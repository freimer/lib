package build

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/airplanedev/lib/pkg/deploy/config"
	"github.com/airplanedev/lib/pkg/deploy/discover/parser"
	"github.com/airplanedev/lib/pkg/utils/fsx"
	"github.com/pkg/errors"
)

const (
	DefaultPythonVersion = BuildTypeVersionPython310
)

func getPythonBuildInstructions(
	root string,
	opts KindOptions,
	shim string,
) (BuildInstructions, error) {
	// Assert that the entrypoint file exists:
	entrypoint, _ := opts["entrypoint"].(string)
	if err := fsx.AssertExistsAll(filepath.Join(root, entrypoint)); err != nil {
		return BuildInstructions{}, err
	}

	installHooks, err := GetInstallHooks(entrypoint, root)
	if err != nil {
		return BuildInstructions{}, err
	}

	return getPythonBuildInstructionsInternal(root, opts, shim, installHooks)
}

func getPythonBundleBuildInstructions(
	root string,
	opts KindOptions,
	shim string,
) (BuildInstructions, error) {
	// Install hooks can only exist in the task root for bundle builds
	installHooks, err := GetInstallHooks("", root)
	if err != nil {
		return BuildInstructions{}, err
	}

	return getPythonBuildInstructionsInternal(root, opts, shim, installHooks)
}
func getPythonBuildInstructionsInternal(
	root string,
	opts KindOptions,
	shim string,
	installHooks installHooks,
) (BuildInstructions, error) {
	if opts["shim"] != "true" {
		return pythonLegacyInstructions(root, opts)
	}

	instructions := []InstallInstruction{
		{
			Cmd: `pip install "airplanesdk>=0.3.0,<0.4.0"`,
		},
	}
	if shim != "" {
		instructions = append(instructions, InstallInstruction{
			Cmd: fmt.Sprintf(`mkdir -p .airplane && %s > .airplane/shim.py`, inlineString(shim)),
		})
	}

	preinstall := []InstallInstruction{}
	postinstall := []InstallInstruction{}
	var airplaneConfig config.AirplaneConfig
	hasAirplaneConfig := fsx.Exists(filepath.Join(root, config.FileName))
	if hasAirplaneConfig {
		var err error
		airplaneConfig, err = config.NewAirplaneConfigFromFile(root)
		if err != nil {
			return BuildInstructions{}, err
		}
		if airplaneConfig.Python.PreInstall != "" {
			preinstall = append(preinstall, InstallInstruction{
				Cmd: airplaneConfig.Python.PreInstall,
			})
		}
		if airplaneConfig.Python.PostInstall != "" {
			postinstall = append(postinstall, InstallInstruction{
				Cmd: airplaneConfig.Python.PostInstall,
			})
		}
	}

	if len(preinstall) == 0 && installHooks.PreInstallFilePath != "" {
		preinstall = append(preinstall, InstallInstruction{
			Cmd:        "./airplane_preinstall.sh",
			SrcPath:    installHooks.PreInstallFilePath,
			DstPath:    "airplane_preinstall.sh",
			Executable: true,
		})
	}
	if len(postinstall) == 0 && installHooks.PostInstallFilePath != "" {
		postinstall = append(postinstall, InstallInstruction{
			Cmd:        "./airplane_postinstall.sh",
			SrcPath:    installHooks.PostInstallFilePath,
			DstPath:    "airplane_postinstall.sh",
			Executable: true,
		})
	}

	instructions = append(instructions, preinstall...)

	requirementsPath := filepath.Join(root, "requirements.txt")
	hasRequirements := fsx.Exists(requirementsPath)
	var embeddedRequirements []string
	var err error
	if hasRequirements {
		instructions = append(instructions, InstallInstruction{
			SrcPath: "requirements.txt",
		})
		embeddedRequirements, err = collectEmbeddedRequirements(root, requirementsPath)
		if err != nil {
			return BuildInstructions{}, err
		}
		for _, embeddedReq := range embeddedRequirements {
			instructions = append(instructions, InstallInstruction{
				SrcPath: embeddedReq,
			})
		}

		if fsx.Exists(filepath.Join(root, "pip.conf")) {
			instructions = append(instructions, InstallInstruction{
				SrcPath: "pip.conf",
			})
		}

		instructions = append(instructions, InstallInstruction{
			Cmd: `pip install -r requirements.txt`,
		})
	}

	instructions = append(instructions, postinstall...)

	return BuildInstructions{
		InstallInstructions: instructions,
	}, nil
}

// Python creates a dockerfile for Python.
func python(
	root string,
	opts KindOptions,
	buildArgs []string,
) (string, error) {
	if opts["shim"] != "true" {
		return pythonLegacy(root, opts)
	}

	// Assert that the entrypoint file exists:
	entrypoint, _ := opts["entrypoint"].(string)
	if err := fsx.AssertExistsAll(filepath.Join(root, entrypoint)); err != nil {
		return "", err
	}

	baseImageType, _ := opts["base"].(BuildBase)
	useSlimImage := baseImageType == BuildBaseSlim
	v, err := GetVersion(NamePython, "3", useSlimImage)
	if err != nil {
		return "", err
	}

	entrypointFunc, _ := opts["entrypointFunc"].(string)
	shim, err := PythonShim(PythonShimParams{
		TaskRoot:       "/airplane",
		Entrypoint:     entrypoint,
		EntrypointFunc: entrypointFunc,
	})
	if err != nil {
		return "", err
	}

	instructions, err := getPythonBuildInstructions(root, opts, shim)
	if err != nil {
		return "", err
	}

	args := make([]string, len(buildArgs))
	for i, a := range buildArgs {
		args[i] = fmt.Sprintf("ARG %s", a)
	}
	argsCommand := strings.Join(args, "\n")

	dockerfileInstructions, err := instructions.DockerfileString()
	if err != nil {
		return "", err
	}

	dockerfile := heredoc.Doc(`
		FROM {{ .Base }}

		# Install common OS dependencies
		RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
			&& apt-get -y install --no-install-recommends \
				libmemcached-dev \
			&& apt-get autoremove -y && apt-get clean -y && rm -rf /var/lib/apt/lists/*

		WORKDIR /airplane
		ENV PIP_CONFIG_FILE=pip.conf

		{{.Args}}

		{{.Instructions}}

		COPY . .
		ENV PYTHONUNBUFFERED=1
		ENTRYPOINT ["python", ".airplane/shim.py"]
	`)

	df, err := applyTemplate(dockerfile, struct {
		Base         string
		Args         string
		Instructions string
	}{
		Base:         v.String(),
		Args:         argsCommand,
		Instructions: dockerfileInstructions,
	})
	if err != nil {
		return "", errors.Wrapf(err, "rendering dockerfile")
	}
	return df, nil
}

func collectEmbeddedRequirements(root, requirementsPath string) ([]string, error) {
	var embeddedRequirements []string
	file, err := os.Open(requirementsPath)
	if err != nil {
		return nil, errors.Wrap(err, "opening requirements.txt")
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		// Embedded requirements are of the form `-r embedded_requirements.txt`.
		if len(parts) == 2 && parts[0] == "-r" {
			embeddedReqPath := parts[1]
			// Ensure the embedded requirements file exists and is in the root.
			if strings.Contains(embeddedReqPath, "..") {
				return nil, errors.New("embedded requirements may not contain directory traversal elements (`..`)")
			}

			if !fsx.Exists(filepath.Join(root, embeddedReqPath)) {
				return nil, errors.Errorf("embedded requirements file %s does not exist", embeddedReqPath)
			}
			embeddedRequirements = append(embeddedRequirements, embeddedReqPath)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, errors.Wrap(err, "reading requirements.txt")
	}

	return embeddedRequirements, nil
}

// Python creates a dockerfile for all Python tasks within a task root.
func pythonBundle(
	root string,
	buildContext BuildContext,
	opts KindOptions,
	buildArgs []string,
	filesToDiscover []string,
) (string, error) {
	if opts["shim"] != "true" {
		return pythonLegacy(root, opts)
	}

	useSlimImage := buildContext.Base == BuildBaseSlim
	v, err := GetVersion(NamePython, string(buildContext.VersionOrDefault()), useSlimImage)
	if err != nil {
		return "", err
	}

	shim, err := UniversalPythonShim("/airplane")
	if err != nil {
		return "", err
	}

	instructions, err := getPythonBundleBuildInstructions(root, opts, shim)
	if err != nil {
		return "", err
	}

	args := make([]string, len(buildArgs))
	for i, a := range buildArgs {
		args[i] = fmt.Sprintf("ARG %s", a)
	}
	argsCommand := strings.Join(args, "\n")

	// Add build tools.
	buildToolsPath := path.Join(root, ".airplane-build-tools")
	if err := os.MkdirAll(buildToolsPath, 0755); err != nil {
		return "", errors.Wrapf(err, "creating build tools path")
	}
	if len(filesToDiscover) > 0 {
		// Generate parser and store on context
		parserPath := path.Join(buildToolsPath, "inlineParser.py")
		if err := os.WriteFile(parserPath, []byte(parser.PythonParserScript), 0755); err != nil {
			return "", errors.Wrap(err, "writing parser script")
		}
	}

	dockerfileInstructions, err := instructions.DockerfileString()
	if err != nil {
		return "", err
	}

	dockerfile := heredoc.Doc(`
		FROM {{ .Base }}

		# Install common OS dependencies
		RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
			&& apt-get -y install --no-install-recommends \
				libmemcached-dev \
			&& apt-get autoremove -y && apt-get clean -y && rm -rf /var/lib/apt/lists/*

		WORKDIR /airplane
		ENV PIP_CONFIG_FILE=pip.conf

		{{.Args}}

		{{.Instructions}}

		COPY . .
		ENV PYTHONUNBUFFERED=1

		{{if .FilesToDiscover}}
		RUN python .airplane-build-tools/inlineParser.py {{.FilesToDiscover}}
		{{end}}
	`)

	df, err := applyTemplate(dockerfile, struct {
		Base            string
		Args            string
		Instructions    string
		FilesToDiscover string
	}{
		Base:            v.String(),
		Args:            argsCommand,
		Instructions:    dockerfileInstructions,
		FilesToDiscover: strings.Join(filesToDiscover, " "),
	})
	if err != nil {
		return "", errors.Wrapf(err, "rendering dockerfile")
	}
	return df, nil
}

//go:embed python-shim.py
var pythonShim string

//go:embed universal-python-shim.py
var universalPythonShim string

type PythonShimParams struct {
	TaskRoot       string
	Entrypoint     string
	EntrypointFunc string
}

// PythonShim generates a shim file for running Python tasks.
func PythonShim(params PythonShimParams) (string, error) {
	shim, err := applyTemplate(pythonShim, struct {
		TaskRoot       string
		Entrypoint     string
		EntrypointFunc string
	}{
		TaskRoot:       backslashEscape(params.TaskRoot, `"`),
		Entrypoint:     backslashEscape(params.Entrypoint, `"`),
		EntrypointFunc: backslashEscape(params.EntrypointFunc, `"`),
	})
	if err != nil {
		return "", errors.Wrapf(err, "rendering shim")
	}

	return shim, nil
}

// UniversalPythonShim generates a shim file for running bundled Python tasks.
func UniversalPythonShim(taskRoot string) (string, error) {
	shim, err := applyTemplate(universalPythonShim, struct {
		TaskRoot string
	}{
		TaskRoot: backslashEscape(taskRoot, `"`),
	})
	if err != nil {
		return "", errors.Wrapf(err, "rendering shim")
	}

	return shim, nil
}

// PythonLegacy generates a dockerfile for legacy python support.
func pythonLegacy(root string, args KindOptions) (string, error) {
	instructions, err := pythonLegacyInstructions(root, args)
	if err != nil {
		return "", err
	}

	var entrypoint, _ = args["entrypoint"].(string)

	var main = filepath.Join(root, entrypoint)
	if err := fsx.AssertExistsAll(main); err != nil {
		return "", err
	}

	t, err := template.New("python").Parse(heredoc.Doc(`
		FROM {{ .Base }}
		WORKDIR /airplane
		{{range .InstallInstructions}}
		{{if .SrcPath}}
		COPY {{.SrcPath}} {{if .DstPath}}{{.DstPath}}{{else}}.{{end}}
		{{if .Executable}}
		RUN chmod +x {{if .DstPath}}{{.DstPath}}{{else}}{{.SrcPath}}{{end}}
		{{end}}
		{{end}}
		{{if .Cmd}}RUN {{.Cmd}}{{end}}
		{{end}}
		ENTRYPOINT ["python", "/airplane/{{ .Entrypoint }}"]
	`))
	if err != nil {
		return "", err
	}

	v, err := GetVersion(NamePython, "3", false)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := t.Execute(&buf, struct {
		Base                string
		Entrypoint          string
		InstallInstructions []InstallInstruction
	}{
		Base:                v.String(),
		Entrypoint:          entrypoint,
		InstallInstructions: instructions.InstallInstructions,
	}); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func pythonLegacyInstructions(root string, args KindOptions) (BuildInstructions, error) {
	instructions := []InstallInstruction{}
	if fsx.AssertExistsAll(filepath.Join(root, "requirements.txt")) != nil {
		instructions = append(instructions,
			InstallInstruction{
				Cmd: "echo > requirements.txt",
			},
		)
	}
	instructions = append(instructions,
		InstallInstruction{
			SrcPath: ".",
		},
	)
	instructions = append(instructions,
		InstallInstruction{
			Cmd: `pip install -r requirements.txt`,
		},
	)

	return BuildInstructions{
		InstallInstructions: instructions,
	}, nil
}
