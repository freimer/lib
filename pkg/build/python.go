package build

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/MakeNowJust/heredoc"
	"github.com/airplanedev/lib/pkg/utils/fsx"
	"github.com/pkg/errors"
)

// Python creates a dockerfile for Python.
func python(root string, opts KindOptions, buildArgs []string) (string, error) {
	if opts["shim"] != "true" {
		return pythonLegacy(root, opts)
	}

	// Assert that the entrypoint file exists:
	entrypoint, _ := opts["entrypoint"].(string)
	if err := fsx.AssertExistsAll(filepath.Join(root, entrypoint)); err != nil {
		return "", err
	}

	v, err := GetVersion(NamePython, "3")
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

	for i, a := range buildArgs {
		buildArgs[i] = fmt.Sprintf("ARG %s", a)
	}
	argsCommand := strings.Join(buildArgs, "\n")

	dockerfile := heredoc.Doc(`
		FROM {{ .Base }}

		# Install common OS dependencies
		RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
			&& apt-get -y install --no-install-recommends \
				libmemcached-dev \
			&& apt-get autoremove -y && apt-get clean -y && rm -rf /var/lib/apt/lists/*

		WORKDIR /airplane
		RUN pip install "airplanesdk>=0.3.0,<0.4.0"
		RUN mkdir -p .airplane && {{.InlineShim}} > .airplane/shim.py

		{{.Args}}

		{{if .HasRequirements}}
		COPY requirements.txt .
		{{if .HasPipConf}}
		COPY pip.conf .
		ENV PIP_CONFIG_FILE=pip.conf
		{{end}}
		RUN pip install -r requirements.txt
		{{end}}
		COPY . .
		ENV PYTHONUNBUFFERED=1
		ENTRYPOINT ["python", ".airplane/shim.py"]
	`)

	df, err := applyTemplate(dockerfile, struct {
		Base            string
		InlineShim      string
		HasRequirements bool
		HasPipConf      bool
		Args            string
	}{
		Base:            v.String(),
		InlineShim:      inlineString(shim),
		HasRequirements: fsx.Exists(filepath.Join(root, "requirements.txt")),
		HasPipConf:      fsx.Exists(filepath.Join(root, "pip.conf")),
		Args:            argsCommand,
	})
	if err != nil {
		return "", errors.Wrapf(err, "rendering dockerfile")
	}

	return df, nil
}

//go:embed python-shim.py
var pythonShim string

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

// PythonLegacy generates a dockerfile for legacy python support.
func pythonLegacy(root string, args KindOptions) (string, error) {
	var entrypoint, _ = args["entrypoint"].(string)
	var main = filepath.Join(root, entrypoint)
	var reqs = filepath.Join(root, "requirements.txt")

	if err := fsx.AssertExistsAll(main); err != nil {
		return "", err
	}

	t, err := template.New("python").Parse(heredoc.Doc(`
		FROM {{ .Base }}
		WORKDIR /airplane
		{{if not .HasRequirements}}
		RUN echo > requirements.txt
		{{end}}
		COPY . .
		RUN pip install -r requirements.txt
		ENTRYPOINT ["python", "/airplane/{{ .Entrypoint }}"]
	`))
	if err != nil {
		return "", err
	}

	v, err := GetVersion(NamePython, "3")
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := t.Execute(&buf, struct {
		Base            string
		Entrypoint      string
		HasRequirements bool
	}{
		Base:            v.String(),
		Entrypoint:      entrypoint,
		HasRequirements: fsx.AssertExistsAll(reqs) == nil,
	}); err != nil {
		return "", err
	}

	return buf.String(), nil
}
