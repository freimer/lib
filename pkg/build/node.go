package build

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/airplanedev/lib/pkg/utils/fsx"
	"github.com/pkg/errors"
)

const DefaultNodeVersion = "18"
const defaultSDKVersion = "0.2.0"
const workflowRuntimePkg = "@airplane/workflow-runtime"

type templateParams struct {
	Workdir                          string
	Base                             string
	HasPackageJSON                   bool
	UsesWorkspaces                   bool
	InlineShim                       string
	InlineShimPackageJSON            string
	InlineWorkflowBundlerScript      string
	InlineWorkflowInterceptorsScript string
	InlineWorkflowShimScript         string
	IsWorkflow                       bool
	NodeVersion                      string
	ExternalFlags                    string
	InstallCommand                   string
	PostInstallCommand               string
	Args                             string
}

// node creates a dockerfile for Node (typescript/javascript).
func node(root string, options KindOptions, buildArgs []string) (string, error) {
	var err error

	// For backwards compatibility, continue to build old Node tasks
	// in the same way. Tasks built with the latest CLI will set
	// shim=true which enables the new code path.
	if shim, ok := options["shim"].(string); !ok || shim != "true" {
		return nodeLegacyBuilder(root, options)
	}

	// Assert that the entrypoint file exists:
	entrypoint, _ := options["entrypoint"].(string)
	if entrypoint == "" {
		return "", errors.New("expected an entrypoint")
	}
	if err := fsx.AssertExistsAll(filepath.Join(root, entrypoint)); err != nil {
		return "", err
	}

	workdir, _ := options["workdir"].(string)
	rootPackageJSON := filepath.Join(root, "package.json")
	hasPackageJSON := fsx.AssertExistsAll(rootPackageJSON) == nil

	pathYarnLock := filepath.Join(root, "yarn.lock")
	isYarn := fsx.AssertExistsAll(pathYarnLock) == nil

	pathPackageLock := filepath.Join(root, "package-lock.json")
	hasPackageLock := fsx.AssertExistsAll(pathPackageLock) == nil

	runtime, ok := options["runtime"]
	var isWorkflow bool

	if ok {
		// Depending on how the options were serialized, the runtime can be
		// either a string or TaskRuntime; handle both.
		switch v := runtime.(type) {
		case string:
			isWorkflow = v == string(TaskRuntimeWorkflow)
		case TaskRuntime:
			isWorkflow = v == TaskRuntimeWorkflow
		default:
		}
	}

	var pkg pkgJSON
	if hasPackageJSON {
		// Check to see if the package.json uses yarn/npm workspaces.
		// If the package.json has a "workspaces" key, it uses workspaces!
		// We want to know this because if we are in a workspace, our install
		// has to honor all the package.json files in the workspace.
		buf, err := os.ReadFile(rootPackageJSON)
		if err != nil {
			return "", errors.Wrapf(err, "node: reading %s", rootPackageJSON)
		}

		if err := json.Unmarshal(buf, &pkg); err != nil {
			return "", fmt.Errorf("node: parsing %s - %w", rootPackageJSON, err)
		}
	}

	for i, a := range buildArgs {
		buildArgs[i] = fmt.Sprintf("ARG %s", a)
	}
	argsCommand := strings.Join(buildArgs, "\n")

	cfg := templateParams{
		Workdir:        workdir,
		HasPackageJSON: hasPackageJSON,
		UsesWorkspaces: len(pkg.Workspaces.workspaces) > 0,
		// esbuild is relatively generous in the node versions it supports:
		// https://esbuild.github.io/api/#target
		NodeVersion:        GetNodeVersion(options),
		PostInstallCommand: pkg.Settings.PostInstallCommand,
		Args:               argsCommand,
		IsWorkflow:         isWorkflow,
	}

	packageJSONs, usesWorkspaces, err := GetPackageJSONs(rootPackageJSON)
	if err != nil {
		return "", err
	}

	if cfg.HasPackageJSON {
		// Workaround to get esbuild to not bundle dependencies.
		// See build.ExternalPackages for details.
		deps, err := ExternalPackages(packageJSONs, usesWorkspaces)
		if err != nil {
			return "", err
		}
		var flags []string
		for _, dep := range deps {
			flags = append(flags, fmt.Sprintf("--external:%s", dep))
		}
		if isWorkflow {
			// Even if these are imported, we need to mark the root packages
			// as external for esbuild to work properly. Esbuild doesn't
			// care about repeats, so no need to dedupe.
			flags = append(flags, "--external:@temporalio", "--external:@swc")
		}

		cfg.ExternalFlags = strings.Join(flags, " ")
	}

	if !strings.HasPrefix(cfg.Workdir, "/") {
		cfg.Workdir = "/" + cfg.Workdir
	}

	cfg.Base, err = getBaseNodeImage(cfg.NodeVersion)
	if err != nil {
		return "", err
	}

	pjson, err := GenShimPackageJSON(packageJSONs, isWorkflow)
	if err != nil {
		return "", err
	}
	cfg.InlineShimPackageJSON = inlineString(string(pjson))

	entrypointFunc, _ := options["entrypointFunc"].(string)
	if isWorkflow {
		cfg.InlineShim = inlineString(workerShim)
		cfg.InlineWorkflowBundlerScript = inlineString(workflowBundlerScript)
		cfg.InlineWorkflowInterceptorsScript = inlineString(workflowInterceptorsScript)

		workflowShim, err := TemplateEntrypoint(workflowShimScript, NodeShimParams{
			Entrypoint:     entrypoint,
			EntrypointFunc: entrypointFunc,
		})
		if err != nil {
			return "", err
		}
		cfg.InlineWorkflowShimScript = inlineString(workflowShim)
	} else {
		shim, err := TemplatedNodeShim(NodeShimParams{
			Entrypoint:     entrypoint,
			EntrypointFunc: entrypointFunc,
		})
		if err != nil {
			return "", err
		}
		cfg.InlineShim = inlineString(shim)
	}

	installCommand := "npm install --production"
	if pkg.Settings.InstallCommand != "" {
		installCommand = pkg.Settings.InstallCommand
	} else if isYarn {
		if yarnBerry, _ := isYarnBerry(rootPackageJSON); yarnBerry {
			// Yarn Berry has removed --non-interactive --production --frozen-lockfile. There
			// is no real replacement for these, so we'll just install with `yarn install`.
			installCommand = "yarn install"
		} else {
			// Because the install command is running in the context of a docker build, the yarn cache
			// isn't used after the packages are installed, and so we clean the cache to keep the
			// image lean. This doesn't apply to Yarn v2 (specifically Plug'n'Play), which uses the
			// cache directory for storing packages.
			installCommand = "yarn install --non-interactive --production --frozen-lockfile && yarn cache clean"
		}
	} else if hasPackageLock {
		// Use npm ci if possible, since it's faster and behaves better:
		// https://docs.npmjs.com/cli/v8/commands/npm-ci
		installCommand = "npm ci --production"
	}
	cfg.InstallCommand = strings.ReplaceAll(installCommand, "\n", "\\n")

	// The following Dockerfile can build both JS and TS tasks. In general, we're
	// aiming for recent EC202x support and for support for import/export syntax.
	// The former is easier, since recent versions of Node have excellent coverage
	// of the ECMAScript spec. The latter could be achieved through ECMAScript
	// modules (ESM), but those are not well-supported within the Node community.
	// Basic functionality of ESM is also still in the experimental stage, such as
	// module resolution for relative paths (f.e. ./main.js vs. ./main). Therefore,
	// we have to fallback to a separate build step to offer import/export support.
	// We have a few options -- f.e. babel, tsc, or swc -- but we go with esbuild
	// since it is native to Go.
	//
	// Down the road, we may want to give customers more control over this build process
	// in which case we could introduce an extra step for performing build commands.
	return applyTemplate(heredoc.Doc(`
		FROM {{.Base}}
		ENV NODE_ENV=production
		WORKDIR /airplane{{.Workdir}}
		# Support setting BUILD_NPM_RC or BUILD_NPM_TOKEN to configure private registry auth
		ARG BUILD_NPM_RC
		ARG BUILD_NPM_TOKEN
		RUN [ -z "${BUILD_NPM_RC}" ] || echo "${BUILD_NPM_RC}" > .npmrc
		RUN [ -z "${BUILD_NPM_TOKEN}" ] || echo "//registry.npmjs.org/:_authToken=${BUILD_NPM_TOKEN}" > .npmrc
		# qemu (on m1 at least) segfaults while looking up a UID/GID for running
		# postinstall scripts. We run as root with --unsafe-perm instead, skipping
		# that lookup. Possibly could fix by building for linux/arm on m1 instead
		# of always building for linux/amd64.
		RUN npm install -g esbuild@0.12 --unsafe-perm
		
		# npm >= 7 will automatically install peer dependencies, even if they're satisfied by the root. This is
		# problematic because we need the @airplane/workflow-runtime package to register the workflow runtime in the
		# runtime map that is utilized by the user's code, and so we explicitly request legacy behavior in this
		# instance, which does not install peer dependencies by default.
		RUN mkdir -p /airplane/.airplane && \
			cd /airplane/.airplane && \
			{{.InlineShimPackageJSON}} > package.json && \
			npm install --legacy-peer-deps

		{{if .HasPackageJSON}}
		COPY package*.json yarn.* /airplane/
		{{else}}
		RUN echo '{}' > /airplane/package.json
		{{end}}

		{{if .UsesWorkspaces}}
		COPY . /airplane
		{{end}}

		{{.Args}}

		RUN {{.InstallCommand}}

		{{if not .UsesWorkspaces}}
		COPY . /airplane
		{{end}}

		{{if .PostInstallCommand}}
		RUN {{.PostInstallCommand}}
		{{end}}

		{{if .IsWorkflow}}
		RUN {{.InlineWorkflowShimScript}} >> /airplane/.airplane/workflow-shim.js
		RUN {{.InlineWorkflowInterceptorsScript}} >> /airplane/.airplane/workflow-interceptors.js
		RUN {{.InlineWorkflowBundlerScript}} >> /airplane/.airplane/workflow-bundler.js
		RUN node /airplane/.airplane/workflow-bundler.js
		{{end}}

		RUN {{.InlineShim}} > /airplane/.airplane/shim.js && \
			esbuild /airplane/.airplane/shim.js \
				--bundle \
				--platform=node {{.ExternalFlags}} \
				--target=node{{.NodeVersion}} \
				--outfile=/airplane/.airplane/dist/shim.js

		ENTRYPOINT ["node", "/airplane/.airplane/dist/shim.js"]
	`), cfg)
}

type shimPackageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
	Type         string            `json:"type,omitempty"`
}

// GenShimPackageJSON generates the `package.json` that contains the dependencies required for the shim to run. If the
// dependency is satisfied by a parent directory (i.e. the user's code), then no need to include it here.
func GenShimPackageJSON(packageJSONs []string, isWorkflow bool) ([]byte, error) {
	deps, err := ListDependenciesFromPackageJSONs(packageJSONs)
	if err != nil {
		return nil, err
	}

	var pjson shimPackageJSON
	if isWorkflow {
		pjson = shimPackageJSON{
			Dependencies: map[string]string{
				"airplane":         defaultSDKVersion,
				workflowRuntimePkg: defaultSDKVersion,
			},
		}
	} else {
		pjson = shimPackageJSON{
			Dependencies: map[string]string{
				"airplane": "^0.2.0",
			},
		}
	}

	// Allow users to override any shim dependencies. Given shim code is bundled
	// with user code, we cannot use separate versions of these dependencies so
	// default to whichever version the user requests.
	for dep := range deps {
		delete(pjson.Dependencies, dep)
	}

	// Always keep the versions of airplane and @airplane/workflow-runtime in sync, unless the task's dependencies
	// explicitly include @airplane/workflow-runtime.
	if isWorkflow {
		if version, containsAirplane := deps["airplane"]; containsAirplane {
			if _, containsWorkflowRuntime := deps[workflowRuntimePkg]; !containsWorkflowRuntime {
				pjson.Dependencies[workflowRuntimePkg] = version
			}
		}
	}

	b, err := json.Marshal(pjson)
	return b, errors.Wrap(err, "marshalling shim dependencies")
}

func GetNodeVersion(opts KindOptions) string {
	if opts == nil || opts["nodeVersion"] == nil {
		return DefaultNodeVersion
	}
	nv, ok := opts["nodeVersion"].(string)
	if !ok {
		return DefaultNodeVersion
	}

	return nv
}

//go:embed node-shim.js
var nodeShim string

//go:embed workflow/worker-shim.js
var workerShim string

//go:embed workflow/workflow-bundler.js
var workflowBundlerScript string

//go:embed workflow/workflow-interceptors.js
var workflowInterceptorsScript string

//go:embed workflow/workflow-shim.js
var workflowShimScript string

type NodeShimParams struct {
	Entrypoint     string
	EntrypointFunc string
}

func TemplatedNodeShim(params NodeShimParams) (string, error) {
	return TemplateEntrypoint(nodeShim, params)
}

func TemplateEntrypoint(script string, params NodeShimParams) (string, error) {
	// Remove the `.ts` suffix if one exists, since tsc doesn't accept
	// import paths with `.ts` endings. `.js` endings are fine.
	entrypoint := strings.TrimSuffix(params.Entrypoint, ".ts")
	// The shim is stored under the .airplane directory.
	entrypoint = filepath.Join("../", entrypoint)
	// Escape for embedding into a string
	entrypoint = backslashEscape(entrypoint, `"`)

	shim, err := applyTemplate(script, struct {
		Entrypoint     string
		EntrypointFunc string
	}{
		Entrypoint:     entrypoint,
		EntrypointFunc: params.EntrypointFunc,
	})
	if err != nil {
		return "", errors.Wrap(err, "templating shim")
	}

	return shim, nil
}

// nodeLegacyBuilder creates a dockerfile for Node (typescript/javascript).
//
// TODO(amir): possibly just run `npm start` instead of exposing lots
// of options to users?
func nodeLegacyBuilder(root string, options KindOptions) (string, error) {
	entrypoint, _ := options["entrypoint"].(string)
	main := filepath.Join(root, entrypoint)
	deps := filepath.Join(root, "package.json")
	yarnlock := filepath.Join(root, "yarn.lock")
	pkglock := filepath.Join(root, "package-lock.json")
	lang, _ := options["language"].(string)
	// `workdir` is fixed usually - `buildWorkdir` is a subdirectory of `workdir` if there's
	// `buildCommand` and is ultimately where `entrypoint` is run from.
	buildCommand, _ := options["buildCommand"].(string)
	buildDir, _ := options["buildDir"].(string)
	workdir := "/airplane"
	buildWorkdir := "/airplane"
	cmds := []string{}

	// Make sure that entrypoint and `package.json` exist.
	if err := fsx.AssertExistsAll(main, deps); err != nil {
		return "", err
	}

	// Determine the install command to use.
	if err := fsx.AssertExistsAll(pkglock); err == nil {
		cmds = append(cmds, `npm install package-lock.json`)
	} else if err := fsx.AssertExistsAll(yarnlock); err == nil {
		cmds = append(cmds, `yarn install`)
	}

	// Language specific.
	switch lang {
	case "typescript":
		if buildDir == "" {
			buildDir = ".airplane"
		}
		cmds = append(cmds, `npm install -g typescript@4.1`)
		cmds = append(cmds, `[ -f tsconfig.json ] || echo '{"include": ["*", "**/*"], "exclude": ["node_modules"]}' >tsconfig.json`)
		cmds = append(cmds, fmt.Sprintf(`rm -rf %s && tsc --outDir %s --rootDir .`, buildDir, buildDir))
		if buildCommand != "" {
			// It's not totally expected, but if you do set buildCommand we'll run it after tsc
			cmds = append(cmds, buildCommand)
		}
		buildWorkdir = path.Join(workdir, buildDir)
		// If entrypoint ends in .ts, replace it with .js
		entrypoint = strings.TrimSuffix(entrypoint, ".ts") + ".js"
	case "javascript":
		if buildCommand != "" {
			cmds = append(cmds, buildCommand)
		}
		if buildDir != "" {
			buildWorkdir = path.Join(workdir, buildDir)
		}
	default:
		return "", errors.Errorf("build: unknown language %q, expected \"javascript\" or \"typescript\"", lang)
	}
	entrypoint = path.Join(buildWorkdir, entrypoint)

	baseImage, err := getBaseNodeImage(GetNodeVersion(options))
	if err != nil {
		return "", err
	}

	return applyTemplate(heredoc.Doc(`
		FROM {{ .Base }}
		WORKDIR {{ .Workdir }}
		# Support setting BUILD_NPM_RC or BUILD_NPM_TOKEN to configure private registry auth
		ARG BUILD_NPM_RC
		ARG BUILD_NPM_TOKEN
		RUN [ -z "${BUILD_NPM_RC}" ] || echo "${BUILD_NPM_RC}" > .npmrc
		RUN [ -z "${BUILD_NPM_TOKEN}" ] || echo "//registry.npmjs.org/:_authToken=${BUILD_NPM_TOKEN}" > .npmrc
		COPY . {{ .Workdir }}
		{{ range .Commands }}
		RUN {{ . }}
		{{ end }}
		WORKDIR {{ .BuildWorkdir }}
		ENTRYPOINT ["node", "{{ .Main }}"]
	`), struct {
		Base         string
		Workdir      string
		BuildWorkdir string
		Commands     []string
		Main         string
	}{
		Base:         baseImage,
		Workdir:      workdir,
		BuildWorkdir: buildWorkdir,
		Commands:     cmds,
		Main:         entrypoint,
	})
}

func getBaseNodeImage(version string) (string, error) {
	if version == "" {
		version = DefaultNodeVersion
	}
	v, err := GetVersion(NameNode, version)
	if err != nil {
		return "", err
	}
	base := v.String()
	if base == "" {
		// Assume the version is already a more-specific version - default to just returning it back
		base = "node:" + version + "-buster"
	}

	return base, nil
}

// Settings represent Airplane specific settings.
type Settings struct {
	Root               string `json:"root"`
	InstallCommand     string `json:"install"`
	PostInstallCommand string `json:"postinstall"`
}

type pkgJSON struct {
	Settings   Settings          `json:"airplane"`
	Workspaces pkgJSONWorkspaces `json:"workspaces"`
}

type pkgJSONWorkspaces struct {
	workspaces []string
}

func (p *pkgJSONWorkspaces) UnmarshalJSON(data []byte) error {
	// Workspaces might be an array of strings...
	var workspaces []string
	if err := json.Unmarshal(data, &workspaces); err == nil {
		p.workspaces = workspaces
		return nil
	}

	// Or it might be an object with an array of strings.
	var workspacesObject struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(data, &workspacesObject); err != nil {
		return err
	}
	p.workspaces = workspacesObject.Packages
	return nil

}
