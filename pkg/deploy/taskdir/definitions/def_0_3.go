package definitions

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"text/template"

	"github.com/airplanedev/lib/pkg/api"
	"github.com/airplanedev/lib/pkg/build"
	"github.com/airplanedev/lib/pkg/utils/fsx"
	"github.com/airplanedev/lib/pkg/utils/pointers"
	"github.com/alessio/shellescape"
	"github.com/flynn/go-shlex"
	"github.com/goccy/go-yaml"
	"github.com/pkg/errors"
	"github.com/xeipuuv/gojsonschema"
)

type Definition_0_3 struct {
	Name        string                    `json:"name"`
	Slug        string                    `json:"slug"`
	Description string                    `json:"description,omitempty"`
	Parameters  []ParameterDefinition_0_3 `json:"parameters,omitempty"`
	Resources   ResourceDefinition_0_3    `json:"resources,omitempty"`

	Image  *ImageDefinition_0_3  `json:"docker,omitempty"`
	Node   *NodeDefinition_0_3   `json:"node,omitempty"`
	Python *PythonDefinition_0_3 `json:"python,omitempty"`
	Shell  *ShellDefinition_0_3  `json:"shell,omitempty"`

	SQL     *SQLDefinition_0_3    `json:"sql,omitempty"`
	REST    *RESTDefinition_0_3   `json:"rest,omitempty"`
	Builtin *BuiltinTaskContainer `json:",inline,omitempty"`

	Configs            []string                 `json:"configs,omitempty"`
	Constraints        map[string]string        `json:"constraints,omitempty"`
	RequireRequests    bool                     `json:"requireRequests,omitempty"`
	AllowSelfApprovals DefaultTrueDefinition    `json:"allowSelfApprovals,omitempty"`
	Timeout            DefaultTimeoutDefinition `json:"timeout,omitempty"`
	Runtime            build.TaskRuntime        `json:"runtime,omitempty"`

	Schedules map[string]ScheduleDefinition_0_3 `json:"schedules,omitempty"`

	buildConfig  build.BuildConfig
	defnFilePath string
}

var _ DefinitionInterface = &Definition_0_3{}

type taskKind_0_3 interface {
	fillInUpdateTaskRequest(context.Context, api.IAPIClient, *api.UpdateTaskRequest, build.BuildConfig, bool) error
	hydrateFromTask(context.Context, api.IAPIClient, *api.Task) error
	setEntrypoint(string) error
	setAbsoluteEntrypoint(string) error
	getAbsoluteEntrypoint() (string, error)
	getKindOptions() (build.KindOptions, error)
	getEntrypoint() (string, error)
	getEnv() (api.TaskEnv, error)
	setEnv(api.TaskEnv) error
	getConfigAttachments() []api.ConfigAttachment
	getResourceAttachments() map[string]string
	getBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase)
	SetBuildVersionBase(build.BuildTypeVersion, build.BuildBase)
}

var _ taskKind_0_3 = &ImageDefinition_0_3{}

type ImageDefinition_0_3 struct {
	Image      string      `json:"image"`
	Entrypoint string      `json:"entrypoint,omitempty"`
	Command    string      `json:"command"`
	EnvVars    api.TaskEnv `json:"envVars,omitempty"`
}

func (d *ImageDefinition_0_3) fillInUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest, bc build.BuildConfig, forBundle bool) error {
	if d.Image != "" {
		req.Image = &d.Image
	}
	if args, err := shlex.Split(d.Command); err != nil {
		return err
	} else {
		req.Arguments = args
	}
	if cmd, err := shlex.Split(d.Entrypoint); err != nil {
		return err
	} else {
		req.Command = cmd
	}
	req.Env = d.EnvVars
	return nil
}

func (d *ImageDefinition_0_3) hydrateFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	if t.Image != nil {
		d.Image = *t.Image
	}
	d.Command = shellescape.QuoteCommand(t.Arguments)
	d.Entrypoint = shellescape.QuoteCommand(t.Command)
	d.EnvVars = t.Env
	return nil
}

func (d *ImageDefinition_0_3) setEntrypoint(entrypoint string) error {
	return ErrNoEntrypoint
}

func (d *ImageDefinition_0_3) setAbsoluteEntrypoint(entrypoint string) error {
	return ErrNoEntrypoint
}

func (d *ImageDefinition_0_3) getAbsoluteEntrypoint() (string, error) {
	return "", ErrNoEntrypoint
}

func (d *ImageDefinition_0_3) getKindOptions() (build.KindOptions, error) {
	return nil, nil
}

func (d *ImageDefinition_0_3) getEntrypoint() (string, error) {
	return "", ErrNoEntrypoint
}

func (d *ImageDefinition_0_3) getEnv() (api.TaskEnv, error) {
	return d.EnvVars, nil
}
func (d *ImageDefinition_0_3) setEnv(e api.TaskEnv) error {
	d.EnvVars = e
	return nil
}

func (d *ImageDefinition_0_3) getConfigAttachments() []api.ConfigAttachment {
	return []api.ConfigAttachment{}
}

func (d *ImageDefinition_0_3) getResourceAttachments() map[string]string {
	return nil
}

func (d *ImageDefinition_0_3) getBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase) {
	return build.NoneBuildType, build.BuildTypeVersionUnspecified, build.BuildBaseNone
}

func (d *ImageDefinition_0_3) SetBuildVersionBase(v build.BuildTypeVersion, b build.BuildBase) {
}

var _ taskKind_0_3 = &NodeDefinition_0_3{}

type NodeDefinition_0_3 struct {
	// Entrypoint is the relative path from the task definition file to the script. It does not
	// apply for inline configured tasks.
	Entrypoint  string          `json:"entrypoint"`
	NodeVersion string          `json:"nodeVersion"`
	EnvVars     api.TaskEnv     `json:"envVars,omitempty"`
	Base        build.BuildBase `json:"base,omitempty"`

	absoluteEntrypoint string `json:"-"`
}

func (d *NodeDefinition_0_3) fillInUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest, bc build.BuildConfig, forBundle bool) error {
	req.Env = d.EnvVars
	if forBundle {
		entrypointFunc, _ := bc["entrypointFunc"].(string)
		entrypoint, _ := bc["entrypoint"].(string)
		if req.Runtime == build.TaskRuntimeWorkflow {
			req.Arguments = []string{
				"{{JSON.stringify(params)}}",
				entrypoint,
				entrypointFunc,
			}
		} else {
			entrypoint := path.Join("/airplane/.airplane/", entrypoint)
			// Ensure that the entyrpoint is a .js file.
			entrypoint = fsx.TrimExtension(entrypoint) + ".js"
			req.Command = []string{"node"}
			req.Arguments = []string{
				"/airplane/.airplane/dist/universal-shim.js",
				entrypoint,
				entrypointFunc,
				"{{JSON.stringify(params)}}",
			}
		}
	}
	return nil
}

func (d *NodeDefinition_0_3) hydrateFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	if v, ok := t.KindOptions["entrypoint"]; ok {
		if sv, ok := v.(string); ok {
			d.Entrypoint = sv
		} else {
			return errors.Errorf("expected string entrypoint, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["nodeVersion"]; ok {
		if sv, ok := v.(string); ok {
			d.NodeVersion = sv
		} else {
			return errors.Errorf("expected string nodeVersion, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["base"]; ok {
		if sv, ok := v.(build.BuildBase); ok {
			d.Base = sv
		} else if sv, ok := v.(string); ok {
			d.Base = build.BuildBase(sv)
		} else {
			return errors.Errorf("expected string base, got %T instead", v)
		}
	}
	d.EnvVars = t.Env
	return nil
}

func (d *NodeDefinition_0_3) setEntrypoint(entrypoint string) error {
	d.Entrypoint = entrypoint
	return nil
}

func (d *NodeDefinition_0_3) setAbsoluteEntrypoint(entrypoint string) error {
	d.absoluteEntrypoint = entrypoint
	return nil
}

func (d *NodeDefinition_0_3) getAbsoluteEntrypoint() (string, error) {
	if d.absoluteEntrypoint == "" {
		return "", ErrNoAbsoluteEntrypoint
	}
	return d.absoluteEntrypoint, nil
}

func (d *NodeDefinition_0_3) getKindOptions() (build.KindOptions, error) {
	ko := build.KindOptions{}
	if d.Entrypoint != "" {
		ko["entrypoint"] = d.Entrypoint
	}
	if d.NodeVersion != "" {
		ko["nodeVersion"] = d.NodeVersion
	}
	if d.Base != "" {
		ko["base"] = d.Base
	}
	return ko, nil
}

func (d *NodeDefinition_0_3) getEntrypoint() (string, error) {
	return d.Entrypoint, nil
}

func (d *NodeDefinition_0_3) getEnv() (api.TaskEnv, error) {
	return d.EnvVars, nil
}

func (d *NodeDefinition_0_3) setEnv(e api.TaskEnv) error {
	d.EnvVars = e
	return nil
}

func (d *NodeDefinition_0_3) getConfigAttachments() []api.ConfigAttachment {
	return []api.ConfigAttachment{}
}

func (d *NodeDefinition_0_3) getResourceAttachments() map[string]string {
	return nil
}

func (d *NodeDefinition_0_3) getBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase) {
	return build.NodeBuildType, build.BuildTypeVersion(d.NodeVersion), d.Base
}

func (d *NodeDefinition_0_3) SetBuildVersionBase(v build.BuildTypeVersion, b build.BuildBase) {
	if d.NodeVersion == "" {
		d.NodeVersion = string(v)
	}
	if d.Base == "" {
		d.Base = b
	}
}

var _ taskKind_0_3 = &PythonDefinition_0_3{}

type PythonDefinition_0_3 struct {
	// Entrypoint is the relative path from the task definition file to the script. It does not
	// apply for inline configured tasks.
	Entrypoint string          `json:"entrypoint"`
	EnvVars    api.TaskEnv     `json:"envVars,omitempty"`
	Base       build.BuildBase `json:"base,omitempty"`
	Version    string          `json:"-"`

	absoluteEntrypoint string `json:"-"`
}

func (d *PythonDefinition_0_3) fillInUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest, bc build.BuildConfig, forBundle bool) error {
	req.Env = d.EnvVars
	if forBundle {
		entrypointFunc, _ := bc["entrypointFunc"].(string)
		req.Command = []string{"python"}
		req.Arguments = []string{
			"/airplane/.airplane/shim.py",
			path.Join("/airplane/", bc["entrypoint"].(string)),
			entrypointFunc,
			"{{JSON.stringify(params)}}",
		}
	}
	return nil
}

func (d *PythonDefinition_0_3) hydrateFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	if v, ok := t.KindOptions["entrypoint"]; ok {
		if sv, ok := v.(string); ok {
			d.Entrypoint = sv
		} else {
			return errors.Errorf("expected string entrypoint, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["version"]; ok {
		if sv, ok := v.(string); ok {
			d.Version = sv
		} else {
			return errors.Errorf("expected string version, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["base"]; ok {
		if sv, ok := v.(build.BuildBase); ok {
			d.Base = sv
		} else if sv, ok := v.(string); ok {
			d.Base = build.BuildBase(sv)
		} else {
			return errors.Errorf("expected string base, got %T instead", v)
		}
	}
	d.EnvVars = t.Env
	return nil
}

func (d *PythonDefinition_0_3) setEntrypoint(entrypoint string) error {
	d.Entrypoint = entrypoint
	return nil
}

func (d *PythonDefinition_0_3) setAbsoluteEntrypoint(entrypoint string) error {
	d.absoluteEntrypoint = entrypoint
	return nil
}

func (d *PythonDefinition_0_3) getAbsoluteEntrypoint() (string, error) {
	if d.absoluteEntrypoint == "" {
		return "", ErrNoAbsoluteEntrypoint
	}
	return d.absoluteEntrypoint, nil
}

func (d *PythonDefinition_0_3) getKindOptions() (build.KindOptions, error) {
	ko := build.KindOptions{}
	if d.Entrypoint != "" {
		ko["entrypoint"] = d.Entrypoint
	}
	if d.Base != "" {
		ko["base"] = d.Base
	}
	if d.Version != "" {
		ko["version"] = d.Version
	}
	return ko, nil
}

func (d *PythonDefinition_0_3) getEntrypoint() (string, error) {
	return d.Entrypoint, nil
}

func (d *PythonDefinition_0_3) getEnv() (api.TaskEnv, error) {
	return d.EnvVars, nil
}

func (d *PythonDefinition_0_3) setEnv(e api.TaskEnv) error {
	d.EnvVars = e
	return nil
}

func (d *PythonDefinition_0_3) getConfigAttachments() []api.ConfigAttachment {
	return []api.ConfigAttachment{}
}

func (d *PythonDefinition_0_3) getResourceAttachments() map[string]string {
	return nil
}

func (d *PythonDefinition_0_3) getBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase) {
	return build.PythonBuildType, build.BuildTypeVersion(d.Version), d.Base
}

func (d *PythonDefinition_0_3) SetBuildVersionBase(v build.BuildTypeVersion, b build.BuildBase) {
	if d.Version == "" {
		d.Version = string(v)
	}
	if d.Base == "" {
		d.Base = b
	}
}

var _ taskKind_0_3 = &ShellDefinition_0_3{}

type ShellDefinition_0_3 struct {
	Entrypoint string      `json:"entrypoint"`
	EnvVars    api.TaskEnv `json:"envVars,omitempty"`

	absoluteEntrypoint string `json:"-"`
}

func (d *ShellDefinition_0_3) fillInUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest, bc build.BuildConfig, forBundle bool) error {
	req.Env = d.EnvVars
	if forBundle {
		req.Command = []string{"bash"}
		req.Arguments = []string{
			".airplane/shim.sh",
			fmt.Sprintf("./%s", bc["entrypoint"].(string)),
		}
		// Pass slug={{slug}} as an array to the shell task
		for _, param := range req.Parameters {
			req.Arguments = append(req.Arguments, fmt.Sprintf("%s={{params.%s}}", param.Slug, param.Slug))
		}
		req.InterpolationMode = pointers.String("jst")
	}

	return nil
}

func (d *ShellDefinition_0_3) hydrateFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	if v, ok := t.KindOptions["entrypoint"]; ok {
		if sv, ok := v.(string); ok {
			d.Entrypoint = sv
		} else {
			return errors.Errorf("expected string entrypoint, got %T instead", v)
		}
	}
	d.EnvVars = t.Env
	return nil
}

func (d *ShellDefinition_0_3) setEntrypoint(entrypoint string) error {
	d.Entrypoint = entrypoint
	return nil
}

func (d *ShellDefinition_0_3) setAbsoluteEntrypoint(entrypoint string) error {
	d.absoluteEntrypoint = entrypoint
	return nil
}

func (d *ShellDefinition_0_3) getAbsoluteEntrypoint() (string, error) {
	if d.absoluteEntrypoint == "" {
		return "", ErrNoAbsoluteEntrypoint
	}
	return d.absoluteEntrypoint, nil
}

func (d *ShellDefinition_0_3) getKindOptions() (build.KindOptions, error) {
	return build.KindOptions{
		"entrypoint": d.Entrypoint,
	}, nil
}

func (d *ShellDefinition_0_3) getEntrypoint() (string, error) {
	return d.Entrypoint, nil
}

func (d *ShellDefinition_0_3) getEnv() (api.TaskEnv, error) {
	return d.EnvVars, nil
}

func (d *ShellDefinition_0_3) setEnv(e api.TaskEnv) error {
	d.EnvVars = e
	return nil
}

func (d *ShellDefinition_0_3) getConfigAttachments() []api.ConfigAttachment {
	return []api.ConfigAttachment{}
}

func (d *ShellDefinition_0_3) getResourceAttachments() map[string]string {
	return nil
}

func (d *ShellDefinition_0_3) getBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase) {
	return build.ShellBuildType, build.BuildTypeVersionUnspecified, build.BuildBaseNone
}

func (d *ShellDefinition_0_3) SetBuildVersionBase(v build.BuildTypeVersion, b build.BuildBase) {
}

var _ taskKind_0_3 = &SQLDefinition_0_3{}

type SQLDefinition_0_3 struct {
	Resource        string                 `json:"resource"`
	Entrypoint      string                 `json:"entrypoint"`
	QueryArgs       map[string]interface{} `json:"queryArgs,omitempty"`
	TransactionMode SQLTransactionMode     `json:"transactionMode,omitempty"`
	Configs         []string               `json:"configs,omitempty"`

	// Contents of Entrypoint, cached
	entrypointContents string `json:"-"`
	absoluteEntrypoint string `json:"-"`
}

type SQLTransactionMode string

var _ yaml.IsZeroer = SQLTransactionMode("")

func (tm SQLTransactionMode) IsZero() bool {
	return tm == "auto" || tm == ""
}

func (tm SQLTransactionMode) Value() string {
	if tm == "" {
		return "auto"
	}
	return string(tm)
}

func (d *SQLDefinition_0_3) GetQuery() (string, error) {
	if d.entrypointContents == "" {
		if d.absoluteEntrypoint == "" {
			return "", ErrNoAbsoluteEntrypoint
		}
		queryBytes, err := os.ReadFile(d.absoluteEntrypoint)
		if err != nil {
			return "", errors.Wrapf(err, "reading SQL entrypoint %s", d.Entrypoint)
		}
		d.entrypointContents = string(queryBytes)
	}
	return d.entrypointContents, nil
}

func (d *SQLDefinition_0_3) fillInUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest, bc build.BuildConfig, forBundle bool) error {
	collection, err := getResourceIDsBySlugAndName(ctx, client)
	if err != nil {
		return err
	}

	// Check slugs first.
	if id, ok := collection.bySlug[d.Resource]; ok {
		req.Resources["db"] = id
	} else if id, ok := collection.byName[d.Resource]; ok {
		req.Resources["db"] = id
	} else {
		return api.ResourceMissingError{Slug: d.Resource}
	}
	return nil
}

func (d *SQLDefinition_0_3) hydrateFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	if resID, ok := t.Resources["db"]; ok {
		resourceSlugsByID, err := getResourceSlugsByID(ctx, client)
		if err != nil {
			return err
		}
		if slug, ok := resourceSlugsByID[resID]; ok {
			d.Resource = slug
		}
	}
	if v, ok := t.KindOptions["entrypoint"]; ok {
		if sv, ok := v.(string); ok {
			d.Entrypoint = sv
		} else {
			return errors.Errorf("expected string entrypoint, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["query"]; ok {
		if sv, ok := v.(string); ok {
			d.entrypointContents = sv
		} else {
			return errors.Errorf("expected string query, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["queryArgs"]; ok {
		if mv, ok := v.(map[string]interface{}); ok {
			d.QueryArgs = mv
		} else {
			return errors.Errorf("expected map queryArgs, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["transactionMode"]; ok {
		if sv, ok := v.(string); ok {
			d.TransactionMode = SQLTransactionMode(sv)
		} else {
			return errors.Errorf("expected string transactionMode, got %T instead", v)
		}
	}

	return nil
}

func (d *SQLDefinition_0_3) setEntrypoint(entrypoint string) error {
	d.Entrypoint = entrypoint
	return nil
}

func (d *SQLDefinition_0_3) setAbsoluteEntrypoint(entrypoint string) error {
	d.absoluteEntrypoint = entrypoint
	return nil
}

func (d *SQLDefinition_0_3) getAbsoluteEntrypoint() (string, error) {
	if d.absoluteEntrypoint == "" {
		return "", ErrNoAbsoluteEntrypoint
	}
	return d.absoluteEntrypoint, nil
}

func (d *SQLDefinition_0_3) getKindOptions() (build.KindOptions, error) {
	query, err := d.GetQuery()
	if err != nil {
		return nil, err
	}
	if d.QueryArgs == nil {
		d.QueryArgs = map[string]interface{}{}
	}
	return build.KindOptions{
		"entrypoint":      d.Entrypoint,
		"query":           query,
		"queryArgs":       d.QueryArgs,
		"transactionMode": d.TransactionMode.Value(),
	}, nil
}

func (d *SQLDefinition_0_3) getEntrypoint() (string, error) {
	return d.Entrypoint, nil
}

func (d *SQLDefinition_0_3) getEnv() (api.TaskEnv, error) {
	return nil, nil
}

func (d *SQLDefinition_0_3) setEnv(e api.TaskEnv) error {
	return nil
}

func (d *SQLDefinition_0_3) getConfigAttachments() []api.ConfigAttachment {
	configAttachments := make([]api.ConfigAttachment, len(d.Configs))
	for i, configName := range d.Configs {
		configAttachments[i] = api.ConfigAttachment{NameTag: configName}
	}

	return configAttachments
}

// Rewrites Resource to be a slug if it's a name.
func (d *SQLDefinition_0_3) normalize(ctx context.Context, client api.IAPIClient) error {
	collection, err := getResourceIDsBySlugAndName(ctx, client)
	if err != nil {
		return err
	}
	slugsByID, err := getResourceSlugsByID(ctx, client)
	if err != nil {
		return err
	}

	// Check slugs first.
	if _, ok := collection.bySlug[d.Resource]; ok {
		return nil
	} else if id, ok := collection.byName[d.Resource]; ok {
		if d.Resource, ok = slugsByID[id]; !ok {
			return errors.Errorf("could not find slug for resource with name: %s (id=%s)", d.Resource, id)
		}
	} else {
		return api.ResourceMissingError{Slug: d.Resource}
	}
	return nil
}

func (d *SQLDefinition_0_3) getResourceAttachments() map[string]string {
	return map[string]string{"db": d.Resource}
}

func (d *SQLDefinition_0_3) getBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase) {
	return build.NoneBuildType, build.BuildTypeVersionUnspecified, build.BuildBaseNone
}
func (d *SQLDefinition_0_3) SetBuildVersionBase(v build.BuildTypeVersion, b build.BuildBase) {
}

var _ taskKind_0_3 = &RESTDefinition_0_3{}

type RESTDefinition_0_3 struct {
	Resource      string                 `json:"resource"`
	Method        string                 `json:"method"`
	Path          string                 `json:"path"`
	URLParams     map[string]interface{} `json:"urlParams,omitempty"`
	Headers       map[string]interface{} `json:"headers,omitempty"`
	BodyType      string                 `json:"bodyType"`
	Body          interface{}            `json:"body,omitempty"`
	FormData      map[string]interface{} `json:"formData,omitempty"`
	RetryFailures interface{}            `json:"retryFailures,omitempty"`
	Configs       []string               `json:"configs,omitempty"`
}

func (d *RESTDefinition_0_3) fillInUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest, bc build.BuildConfig, forBundle bool) error {
	collection, err := getResourceIDsBySlugAndName(ctx, client)
	if err != nil {
		return err
	}

	// Check slugs first.
	if id, ok := collection.bySlug[d.Resource]; ok {
		req.Resources["rest"] = id
	} else if id, ok := collection.byName[d.Resource]; ok {
		req.Resources["rest"] = id
	} else {
		return api.ResourceMissingError{Slug: d.Resource}
	}
	return nil
}

func (d *RESTDefinition_0_3) hydrateFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	if resID, ok := t.Resources["rest"]; ok {
		resourceSlugsByID, err := getResourceSlugsByID(ctx, client)
		if err != nil {
			return err
		}
		if slug, ok := resourceSlugsByID[resID]; ok {
			d.Resource = slug
		}
	}
	if v, ok := t.KindOptions["method"]; ok {
		if sv, ok := v.(string); ok {
			d.Method = sv
		} else {
			return errors.Errorf("expected string method, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["path"]; ok {
		if sv, ok := v.(string); ok {
			d.Path = sv
		} else {
			return errors.Errorf("expected string path, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["urlParams"]; ok {
		if mv, ok := v.(map[string]interface{}); ok {
			d.URLParams = mv
		} else {
			return errors.Errorf("expected map urlParams, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["headers"]; ok {
		if mv, ok := v.(map[string]interface{}); ok {
			d.Headers = mv
		} else {
			return errors.Errorf("expected map headers, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["bodyType"]; ok {
		if sv, ok := v.(string); ok {
			d.BodyType = sv
		} else {
			return errors.Errorf("expected string bodyType, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["body"]; ok {
		d.Body = v
	}
	if v, ok := t.KindOptions["formData"]; ok {
		if mv, ok := v.(map[string]interface{}); ok {
			d.FormData = mv
		} else {
			return errors.Errorf("expected map formData, got %T instead", v)
		}
	}
	if v, ok := t.KindOptions["retryFailures"]; ok {
		d.RetryFailures = v
	}

	return nil
}

func (d *RESTDefinition_0_3) setEntrypoint(entrypoint string) error {
	return ErrNoEntrypoint
}

func (d *RESTDefinition_0_3) setAbsoluteEntrypoint(entrypoint string) error {
	return ErrNoEntrypoint
}

func (d *RESTDefinition_0_3) getAbsoluteEntrypoint() (string, error) {
	return "", ErrNoEntrypoint
}

func (d *RESTDefinition_0_3) getKindOptions() (build.KindOptions, error) {
	if d.URLParams == nil {
		d.URLParams = map[string]interface{}{}
	}
	if d.Headers == nil {
		d.Headers = map[string]interface{}{}
	}
	if d.FormData == nil {
		d.FormData = map[string]interface{}{}
	}
	return build.KindOptions{
		"method":        d.Method,
		"path":          d.Path,
		"urlParams":     d.URLParams,
		"headers":       d.Headers,
		"bodyType":      d.BodyType,
		"body":          d.Body,
		"formData":      d.FormData,
		"retryFailures": d.RetryFailures,
	}, nil
}

func (d *RESTDefinition_0_3) getEntrypoint() (string, error) {
	return "", ErrNoEntrypoint
}

func (d *RESTDefinition_0_3) getEnv() (api.TaskEnv, error) {
	return nil, nil
}
func (d *RESTDefinition_0_3) setEnv(e api.TaskEnv) error {
	return nil
}

func (d *RESTDefinition_0_3) getConfigAttachments() []api.ConfigAttachment {
	configAttachments := make([]api.ConfigAttachment, len(d.Configs))
	for i, configName := range d.Configs {
		configAttachments[i] = api.ConfigAttachment{NameTag: configName}
	}

	return configAttachments
}

// Rewrites Resource to be a slug if it's a name.
func (d *RESTDefinition_0_3) normalize(ctx context.Context, client api.IAPIClient) error {
	collection, err := getResourceIDsBySlugAndName(ctx, client)
	if err != nil {
		return err
	}
	slugsByID, err := getResourceSlugsByID(ctx, client)
	if err != nil {
		return err
	}

	// Check slugs first.
	if _, ok := collection.bySlug[d.Resource]; ok {
		return nil
	} else if id, ok := collection.byName[d.Resource]; ok {
		if d.Resource, ok = slugsByID[id]; !ok {
			return errors.Errorf("could not find slug for resource with name: %s (id=%s)", d.Resource, id)
		}
	} else {
		return api.ResourceMissingError{Slug: d.Resource}
	}
	return nil
}

func (d *RESTDefinition_0_3) getResourceAttachments() map[string]string {
	return map[string]string{"rest": d.Resource}
}

func (d *RESTDefinition_0_3) getBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase) {
	return build.NoneBuildType, build.BuildTypeVersionUnspecified, build.BuildBaseNone
}
func (d *RESTDefinition_0_3) SetBuildVersionBase(v build.BuildTypeVersion, b build.BuildBase) {
}

type ParameterDefinition_0_3 struct {
	Name        string                 `json:"name"`
	Slug        string                 `json:"slug"`
	Type        string                 `json:"type"`
	Description string                 `json:"description,omitempty"`
	Default     interface{}            `json:"default,omitempty"`
	Required    DefaultTrueDefinition  `json:"required,omitempty"`
	Options     []OptionDefinition_0_3 `json:"options,omitempty"`
	Regex       string                 `json:"regex,omitempty"`
}

type OptionDefinition_0_3 struct {
	Label  string      `json:"label"`
	Value  interface{} `json:"value,omitempty"`
	Config *string     `json:"config,omitempty"`
}

var _ json.Unmarshaler = &OptionDefinition_0_3{}

func (o *OptionDefinition_0_3) UnmarshalJSON(b []byte) error {
	// If it's just a string, dump it in the value field.
	var value string
	if err := json.Unmarshal(b, &value); err == nil {
		o.Value = value
		return nil
	}

	// Otherwise, perform a normal unmarshal operation.
	// Note we need a new type, otherwise we recursively call this
	// method and end up stack overflowing.
	type option OptionDefinition_0_3
	var opt option
	if err := json.Unmarshal(b, &opt); err != nil {
		return err
	}
	*o = OptionDefinition_0_3(opt)

	return nil
}

type ScheduleDefinition_0_3 struct {
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	CronExpr    string                 `json:"cron"`
	ParamValues map[string]interface{} `json:"paramValues,omitempty"`
}

//go:embed schema_0_3.json
var schemaStr string

func NewDefinition_0_3(name string, slug string, kind build.TaskKind, entrypoint string) (Definition_0_3, error) {
	def := Definition_0_3{
		Name: name,
		Slug: slug,
	}

	switch kind {
	case build.TaskKindImage:
		def.Image = &ImageDefinition_0_3{
			Image:   "alpine:3",
			Command: `echo "hello world"`,
		}
	case build.TaskKindNode:
		def.Node = &NodeDefinition_0_3{
			Entrypoint:  entrypoint,
			NodeVersion: string(build.DefaultNodeVersion),
		}
	case build.TaskKindPython:
		def.Python = &PythonDefinition_0_3{
			Entrypoint: entrypoint,
		}
	case build.TaskKindShell:
		def.Shell = &ShellDefinition_0_3{
			Entrypoint: entrypoint,
		}
	case build.TaskKindSQL:
		def.SQL = &SQLDefinition_0_3{
			Entrypoint: entrypoint,
		}
	case build.TaskKindREST:
		def.REST = &RESTDefinition_0_3{
			Method:   "POST",
			Path:     "/",
			BodyType: "json",
			Body:     "{}",
		}
	case build.TaskKindBuiltin:
		return Definition_0_3{}, errors.New("use NewBuiltinDefinition_0_3 instead")
	default:
		return Definition_0_3{}, errors.Errorf("unknown kind: %s", kind)
	}

	return def, nil
}

func NewBuiltinDefinition_0_3(name string, slug string, builtin BuiltinTaskDef) (Definition_0_3, error) {
	return Definition_0_3{
		Name:    name,
		Slug:    slug,
		Builtin: &BuiltinTaskContainer{def: builtin},
	}, nil
}

// Customize the UnmarshalJSON to pull out the builtin, if there is any. The MarshalJSON
// customization is done on the BuiltinTaskContainer (as this field is inlined).
func (d *Definition_0_3) UnmarshalJSON(b []byte) error {
	// Perform a normal unmarshal operation.
	// Note we need a new type, otherwise we recursively call this
	// method and end up stack overflowing.
	type definition Definition_0_3
	var def definition
	if err := json.Unmarshal(b, &def); err != nil {
		return err
	}
	*d = Definition_0_3(def)

	// Unmarshal it into a map.
	var serialized map[string]interface{}
	if err := json.Unmarshal(b, &serialized); err != nil {
		return err
	}

	// Is there a builtin somewhere?
	for key, plugin := range builtinTaskPluginsByDefinitionKey {
		defMap, ok := serialized[key]
		if !ok {
			continue
		}
		defBytes, err := json.Marshal(defMap)
		if err != nil {
			return err
		}
		kind := plugin.GetTaskKindDefinition()
		if err := json.Unmarshal(defBytes, &kind); err != nil {
			return err
		}
		d.Builtin = &BuiltinTaskContainer{def: kind}
		break
	}

	return nil
}

func (d Definition_0_3) Marshal(format DefFormat) ([]byte, error) {
	switch format {
	case DefFormatYAML:
		// Use the JSON marshaler so we use MarshalJSON methods.
		buf, err := yaml.MarshalWithOptions(d,
			yaml.UseJSONMarshaler(),
			yaml.UseLiteralStyleIfMultiline(true))
		if err != nil {
			return nil, err
		}
		return buf, nil

	case DefFormatJSON:
		// Use the YAML marshaler so we can take advantage of the yaml.IsZeroer check on omitempty.
		// But make it use the JSON marshaler so we use MarshalJSON methods.
		buf, err := yaml.MarshalWithOptions(d,
			yaml.UseJSONMarshaler(),
			yaml.JSON())
		if err != nil {
			return nil, err
		}
		// `yaml.Marshal` doesn't allow configuring JSON indentation, so do it after the fact.
		var out bytes.Buffer
		if err := json.Indent(&out, buf, "", "\t"); err != nil {
			return nil, err
		}
		return out.Bytes(), nil

	default:
		return nil, errors.Errorf("unknown format: %s", format)
	}
}

// GenerateCommentedFile generates a commented YAML file under certain circumstances. If the format
// requested isn't YAML, or if the definition has other things filled in, this method defaults to
// calling Marshal(format).
func (d Definition_0_3) GenerateCommentedFile(format DefFormat) ([]byte, error) {
	// If it's not YAML, or you have other things defined on your task def, bail.
	if format != DefFormatYAML ||
		d.Description != "" ||
		len(d.Parameters) > 0 ||
		len(d.Resources.Attachments) > 0 ||
		len(d.Constraints) > 0 ||
		d.RequireRequests ||
		!d.AllowSelfApprovals.IsZero() ||
		!d.Timeout.IsZero() ||
		d.Builtin != nil {
		return d.Marshal(format)
	}

	kind, err := d.Kind()
	if err != nil {
		return nil, err
	}

	taskDefinition := new(bytes.Buffer)
	var paramsExtraInfo string
	switch kind {
	case build.TaskKindImage:
		if d.Image.Entrypoint != "" || len(d.Image.EnvVars) > 0 {
			return d.Marshal(format)
		}
		tmpl, err := template.New("image").Parse(imageTemplate)
		if err != nil {
			return nil, errors.Wrap(err, "parsing image template")
		}
		if err := tmpl.Execute(taskDefinition, d.Image); err != nil {
			return nil, errors.Wrap(err, "executing image template")
		}
		paramsExtraInfo = imageParamsExtraDescription
	case build.TaskKindNode:
		if len(d.Node.EnvVars) > 0 {
			return d.Marshal(format)
		}
		tmpl, err := template.New("node").Parse(nodeTemplate)
		if err != nil {
			return nil, errors.Wrap(err, "parsing node template")
		}
		if err := tmpl.Execute(taskDefinition, d.Node); err != nil {
			return nil, errors.Wrap(err, "executing node template")
		}
	case build.TaskKindPython:
		if len(d.Python.EnvVars) > 0 {
			return d.Marshal(format)
		}
		tmpl, err := template.New("python").Parse(pythonTemplate)
		if err != nil {
			return nil, errors.Wrap(err, "parsing python template")
		}
		if err := tmpl.Execute(taskDefinition, d.Python); err != nil {
			return nil, errors.Wrap(err, "executing python template")
		}
	case build.TaskKindShell:
		if len(d.Shell.EnvVars) > 0 {
			return d.Marshal(format)
		}
		tmpl, err := template.New("shell").Parse(shellTemplate)
		if err != nil {
			return nil, errors.Wrap(err, "parsing shell template")
		}
		if err := tmpl.Execute(taskDefinition, d.Shell); err != nil {
			return nil, errors.Wrap(err, "executing shell template")
		}
		paramsExtraInfo = shellParamsExtraDescription
	case build.TaskKindSQL:
		if d.SQL.Resource != "" || len(d.SQL.QueryArgs) > 0 {
			return d.Marshal(format)
		}
		tmpl, err := template.New("sql").Parse(sqlTemplate)
		if err != nil {
			return nil, errors.Wrap(err, "parsing SQL template")
		}
		if err := tmpl.Execute(taskDefinition, d.SQL); err != nil {
			return nil, errors.Wrap(err, "executing sql template")
		}
	case build.TaskKindREST:
		if d.REST.Resource != "" ||
			len(d.REST.URLParams) > 0 ||
			len(d.REST.Headers) > 0 ||
			len(d.REST.FormData) > 0 {
			return d.Marshal(format)
		}
		tmpl, err := template.New("rest").Parse(restTemplate)
		if err != nil {
			return nil, errors.Wrap(err, "parsing REST template")
		}
		if err := tmpl.Execute(taskDefinition, d.REST); err != nil {
			return nil, errors.Wrap(err, "executing rest template")
		}
	default:
		return d.Marshal(format)
	}

	// Remove any newlines from the name & run yaml.Marshal to take care of any weird characters.
	nameBuf, err := yaml.Marshal(strings.ReplaceAll(d.Name, "\n", ""))
	if err != nil {
		return nil, errors.Wrap(err, "marshalling name")
	}
	// yaml.Marshal always appends a newline, trim it.
	name := strings.TrimSuffix(string(nameBuf), "\n")

	tmpl, err := template.New("definition").Parse(definitionTemplate)
	if err != nil {
		return nil, errors.Wrap(err, "parsing definition template")
	}
	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, map[string]interface{}{
		"slug":                   d.Slug,
		"name":                   name,
		"taskDefinition":         taskDefinition.String(),
		"paramsExtraDescription": paramsExtraInfo,
	}); err != nil {
		return nil, errors.Wrap(err, "executing definition template")
	}
	return buf.Bytes(), nil
}

func (d *Definition_0_3) Unmarshal(format DefFormat, buf []byte) error {
	var err error
	switch format {
	case DefFormatYAML:
		buf, err = yaml.YAMLToJSON(buf)
		if err != nil {
			return err
		}
	case DefFormatJSON:
		// nothing
	default:
		return errors.Errorf("unknown format: %s", format)
	}

	schemaLoader := gojsonschema.NewStringLoader(schemaStr)
	docLoader := gojsonschema.NewBytesLoader(buf)

	result, err := gojsonschema.Validate(schemaLoader, docLoader)
	if err != nil {
		return errors.Wrap(err, "validating schema")
	}

	if !result.Valid() {
		return errors.WithStack(ErrSchemaValidation{Errors: result.Errors()})
	}

	if err = json.Unmarshal(buf, &d); err != nil {
		return err
	}
	return nil
}

func (d *Definition_0_3) Normalize(ctx context.Context, client api.IAPIClient) error {
	// Rewrites Resource to be a slug rather than a name.
	if d.SQL != nil {
		return d.SQL.normalize(ctx, client)
	} else if d.REST != nil {
		return d.REST.normalize(ctx, client)
	}
	return nil
}

func (d *Definition_0_3) SetAbsoluteEntrypoint(entrypoint string) error {
	taskKind, err := d.taskKind()
	if err != nil {
		return err
	}

	return taskKind.setAbsoluteEntrypoint(entrypoint)
}

func (d *Definition_0_3) GetAbsoluteEntrypoint() (string, error) {
	taskKind, err := d.taskKind()
	if err != nil {
		return "", err
	}

	return taskKind.getAbsoluteEntrypoint()
}

func (d Definition_0_3) Kind() (build.TaskKind, error) {
	if d.Image != nil {
		return build.TaskKindImage, nil
	} else if d.Node != nil {
		return build.TaskKindNode, nil
	} else if d.Python != nil {
		return build.TaskKindPython, nil
	} else if d.Shell != nil {
		return build.TaskKindShell, nil
	} else if d.SQL != nil {
		return build.TaskKindSQL, nil
	} else if d.REST != nil {
		return build.TaskKindREST, nil
	} else if d.Builtin != nil {
		return build.TaskKindBuiltin, nil
	} else {
		return "", errors.New("incomplete task definition")
	}
}

func (d Definition_0_3) taskKind() (taskKind_0_3, error) {
	if d.Image != nil {
		return d.Image, nil
	} else if d.Node != nil {
		return d.Node, nil
	} else if d.Python != nil {
		return d.Python, nil
	} else if d.Shell != nil {
		return d.Shell, nil
	} else if d.SQL != nil {
		return d.SQL, nil
	} else if d.REST != nil {
		return d.REST, nil
	} else if d.Builtin != nil {
		return d.Builtin.def, nil
	} else {
		return nil, errors.New("incomplete task definition")
	}
}

func (d Definition_0_3) GetUpdateTaskRequest(ctx context.Context, client api.IAPIClient, forBundle bool) (api.UpdateTaskRequest, error) {
	req := api.UpdateTaskRequest{
		Slug:        d.Slug,
		Name:        d.Name,
		Description: d.Description,
		Timeout:     d.Timeout.Value(),
		Runtime:     d.Runtime,
		ExecuteRules: api.UpdateExecuteRulesRequest{
			RequireRequests: &d.RequireRequests,
		},
		Resources: make(map[string]string),
	}

	params, err := d.GetParameters()
	if err != nil {
		return api.UpdateTaskRequest{}, err
	}
	req.Parameters = params

	if err := d.addResourcesToUpdateTaskRequest(ctx, client, &req); err != nil {
		return api.UpdateTaskRequest{}, err
	}

	if len(d.Configs) > 0 {
		configAttachments := make([]api.ConfigAttachment, len(d.Configs))
		for i, configName := range d.Configs {
			configAttachments[i] = api.ConfigAttachment{NameTag: configName}
		}
		req.Configs = &configAttachments
	}

	if len(d.Constraints) > 0 {
		labels := []api.AgentLabel{}
		for key, val := range d.Constraints {
			labels = append(labels, api.AgentLabel{
				Key:   key,
				Value: val,
			})
		}
		req.Constraints = api.RunConstraints{
			Labels: labels,
		}
	}

	req.ExecuteRules.DisallowSelfApprove = pointers.Bool(!d.AllowSelfApprovals.Value())

	bc, err := d.GetBuildConfig()
	if err != nil {
		return api.UpdateTaskRequest{}, err
	}
	if err := d.addKindSpecificsToUpdateTaskRequest(ctx, client, &req, bc, forBundle); err != nil {
		return api.UpdateTaskRequest{}, err
	}

	return req, nil
}

func (d Definition_0_3) addResourcesToUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest) error {
	if len(d.Resources.Attachments) == 0 {
		return nil
	}

	collection, err := getResourceIDsBySlugAndName(ctx, client)
	if err != nil {
		return errors.Wrap(err, "fetching resources")
	}

	for alias, ref := range d.Resources.Attachments {
		if id, ok := collection.bySlug[ref]; ok {
			req.Resources[alias] = id
		} else {
			return api.ResourceMissingError{Slug: ref}
		}
	}

	return nil
}

func (d Definition_0_3) addKindSpecificsToUpdateTaskRequest(ctx context.Context, client api.IAPIClient, req *api.UpdateTaskRequest, bc build.BuildConfig, forBundle bool) error {
	kind, options, err := d.GetKindAndOptions()
	if err != nil {
		return err
	}
	req.Kind = kind
	req.KindOptions = options

	env, err := d.GetEnv()
	if err != nil {
		return err
	}
	req.Env = env

	if req.Configs == nil || len(*req.Configs) == 0 {
		configAttachments, err := d.GetConfigAttachments()
		if err != nil {
			return err
		}
		req.Configs = &configAttachments
	}

	taskKind, err := d.taskKind()
	if err != nil {
		return err
	}
	if err := taskKind.fillInUpdateTaskRequest(ctx, client, req, bc, forBundle); err != nil {
		return err
	}
	return nil
}

func (d Definition_0_3) Entrypoint() (string, error) {
	taskKind, err := d.taskKind()
	if err != nil {
		return "", err
	}
	return taskKind.getEntrypoint()
}

func (d Definition_0_3) GetDefnFilePath() string {
	return d.defnFilePath
}

func (d Definition_0_3) GetDescription() string {
	return d.Description
}

func (d Definition_0_3) GetParameters() (api.Parameters, error) {
	return convertParametersDefToAPI(d.Parameters)
}

func (d Definition_0_3) GetBuildType() (build.BuildType, build.BuildTypeVersion, build.BuildBase, error) {
	taskKind, err := d.taskKind()
	if err != nil {
		return "", "", "", err
	}
	t, v, b := taskKind.getBuildType()
	return t, v, b, nil
}

func (d Definition_0_3) SetBuildVersionBase(v build.BuildTypeVersion, b build.BuildBase) error {
	taskKind, err := d.taskKind()
	if err != nil {
		return err
	}
	taskKind.SetBuildVersionBase(v, b)
	return nil
}

func (d *Definition_0_3) SetDefnFilePath(filePath string) {
	d.defnFilePath = filePath
}

func (d *Definition_0_3) UpgradeJST() error {
	return nil
}

func (d *Definition_0_3) GetKindAndOptions() (build.TaskKind, build.KindOptions, error) {
	kind, err := d.Kind()
	if err != nil {
		return "", nil, err
	}

	taskKind, err := d.taskKind()
	if err != nil {
		return "", nil, err
	}

	options, err := taskKind.getKindOptions()
	if err != nil {
		return "", nil, err
	}

	return kind, options, nil
}

func (d *Definition_0_3) GetEnv() (api.TaskEnv, error) {
	taskKind, err := d.taskKind()
	if err != nil {
		return nil, err
	}
	return taskKind.getEnv()
}

func (d *Definition_0_3) SetEnv(e api.TaskEnv) error {
	taskKind, err := d.taskKind()
	if err != nil {
		return err
	}
	return taskKind.setEnv(e)
}

func (d *Definition_0_3) GetConfigAttachments() ([]api.ConfigAttachment, error) {
	taskKind, err := d.taskKind()
	if err != nil {
		return nil, err
	}
	return taskKind.getConfigAttachments(), nil
}

func (d *Definition_0_3) GetResourceAttachments() (map[string]string, error) {
	taskKind, err := d.taskKind()
	if err != nil {
		return nil, err
	}

	taskKindResourceAttachments := taskKind.getResourceAttachments()
	resourceAttachments := make(map[string]string, len(d.Resources.Attachments)+len(taskKindResourceAttachments))
	// Append explicit resource attachments.
	for alias, id := range d.Resources.Attachments {
		resourceAttachments[alias] = id
	}

	// Append kind-specific resource attachments - these override any explicit resource attachments above
	for alias, id := range taskKindResourceAttachments {
		resourceAttachments[alias] = id
	}

	return resourceAttachments, nil
}

func (d *Definition_0_3) GetSlug() string {
	return d.Slug
}

func (d *Definition_0_3) GetName() string {
	return d.Name
}

func (d *Definition_0_3) GetRuntime() build.TaskRuntime {
	return d.Runtime
}

func (d *Definition_0_3) SetEntrypoint(entrypoint string) error {
	taskKind, err := d.taskKind()
	if err != nil {
		return err
	}

	return taskKind.setEntrypoint(entrypoint)
}

func (d *Definition_0_3) SetWorkdir(taskroot, workdir string) error {
	// TODO: currently only a concept on Node - should be generalized to all builders.
	if d.Node == nil {
		return nil
	}

	d.SetBuildConfig("workdir", strings.TrimPrefix(workdir, taskroot))

	return nil
}

func (d *Definition_0_3) GetSchedules() map[string]api.Schedule {
	if len(d.Schedules) == 0 {
		return nil
	}

	schedules := make(map[string]api.Schedule)
	for slug, def := range d.Schedules {
		schedules[slug] = api.Schedule{
			Name:        def.Name,
			Description: def.Description,
			CronExpr:    def.CronExpr,
			ParamValues: def.ParamValues,
		}
	}
	return schedules
}

func NewDefinitionFromTask_0_3(ctx context.Context, client api.IAPIClient, t api.Task) (Definition_0_3, error) {
	d := Definition_0_3{
		Name:            t.Name,
		Slug:            t.Slug,
		Description:     t.Description,
		RequireRequests: t.ExecuteRules.RequireRequests,
		Runtime:         t.Runtime,
	}

	params, err := convertParametersAPIToDef(t.Parameters)
	if err != nil {
		return Definition_0_3{}, err
	}
	d.Parameters = params

	if err := d.convertResourcesFromTask(ctx, client, &t); err != nil {
		return Definition_0_3{}, err
	}

	if err := d.convertTaskKindFromTask(ctx, client, &t); err != nil {
		return Definition_0_3{}, err
	}

	if len(t.Configs) > 0 {
		d.Configs = make([]string, len(t.Configs))
		for idx, config := range t.Configs {
			d.Configs[idx] = config.NameTag
		}
	}

	if !t.Constraints.IsEmpty() {
		d.Constraints = map[string]string{}
		for _, label := range t.Constraints.Labels {
			d.Constraints[label.Key] = label.Value
		}
	}

	d.AllowSelfApprovals.value = pointers.Bool(!t.ExecuteRules.DisallowSelfApprove)
	d.Timeout.value = t.Timeout

	schedules := make(map[string]ScheduleDefinition_0_3)
	for _, trigger := range t.Triggers {
		if trigger.Kind != api.TriggerKindSchedule || trigger.Slug == nil {
			// Trigger is not a schedule deployed via code
			continue
		}
		if trigger.ArchivedAt != nil || trigger.DisabledAt != nil {
			// Trigger is archived or disabled, so don't add to task defn file
			continue
		}

		schedules[*trigger.Slug] = ScheduleDefinition_0_3{
			Name:        trigger.Name,
			Description: trigger.Description,
			CronExpr:    trigger.KindConfig.Schedule.CronExpr.String(),
			ParamValues: trigger.KindConfig.Schedule.ParamValues,
		}
	}
	if len(schedules) > 0 {
		d.Schedules = schedules
	}

	return d, nil
}

func (d *Definition_0_3) convertResourcesFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	if len(t.Resources) == 0 {
		return nil
	}

	resourceSlugsByID, err := getResourceSlugsByID(ctx, client)
	if err != nil {
		return errors.Wrap(err, "fetching resources")
	}

	d.Resources.Attachments = make(map[string]string)
	for alias, id := range t.Resources {
		// Ignore SQL/REST resources; they get routed elsewhere.
		if (t.Kind == build.TaskKindSQL && alias == "db") ||
			(t.Kind == build.TaskKindREST && alias == "rest") ||
			(t.Kind == build.TaskKindBuiltin) {
			continue
		}
		slug, ok := resourceSlugsByID[id]
		if ok {
			d.Resources.Attachments[alias] = slug
		}
	}

	return nil
}

func (d *Definition_0_3) convertTaskKindFromTask(ctx context.Context, client api.IAPIClient, t *api.Task) error {
	switch t.Kind {
	case build.TaskKindImage:
		d.Image = &ImageDefinition_0_3{}
		return d.Image.hydrateFromTask(ctx, client, t)
	case build.TaskKindNode:
		d.Node = &NodeDefinition_0_3{}
		return d.Node.hydrateFromTask(ctx, client, t)
	case build.TaskKindPython:
		d.Python = &PythonDefinition_0_3{}
		return d.Python.hydrateFromTask(ctx, client, t)
	case build.TaskKindShell:
		d.Shell = &ShellDefinition_0_3{}
		return d.Shell.hydrateFromTask(ctx, client, t)
	case build.TaskKindSQL:
		d.SQL = &SQLDefinition_0_3{}
		return d.SQL.hydrateFromTask(ctx, client, t)
	case build.TaskKindREST:
		d.REST = &RESTDefinition_0_3{}
		return d.REST.hydrateFromTask(ctx, client, t)
	case build.TaskKindBuiltin:
		return hydrateBuiltin(ctx, client, d, t)
	default:
		return errors.Errorf("unknown task kind: %s", t.Kind)
	}
}

func (d *Definition_0_3) GetBuildConfig() (build.BuildConfig, error) {
	config := build.BuildConfig{}

	_, options, err := d.GetKindAndOptions()
	if err != nil {
		return nil, err
	}
	for key, val := range options {
		config[key] = val
	}

	// Pass runtime through to builder
	config["runtime"] = d.Runtime

	for key, val := range d.buildConfig {
		if val == nil { // Nil masks out the value.
			delete(config, key)
		} else {
			config[key] = val
		}
	}

	return config, nil
}

func (d *Definition_0_3) SetBuildConfig(key string, value interface{}) {
	if d.buildConfig == nil {
		d.buildConfig = map[string]interface{}{}
	}
	d.buildConfig[key] = value
}

type ResourceDefinition_0_3 struct {
	Attachments map[string]string
}

func (r *ResourceDefinition_0_3) UnmarshalJSON(b []byte) error {
	// If it's just a map, dump it in the Attachments field.
	if err := json.Unmarshal(b, &r.Attachments); err == nil {
		return nil
	}

	// Otherwise, expect a list.
	var list []interface{}
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}

	r.Attachments = make(map[string]string)
	for _, item := range list {
		if s, ok := item.(string); ok {
			if _, exists := r.Attachments[s]; exists {
				return errors.New("aliases in resource list must be unique")
			}
			r.Attachments[s] = s
		} else {
			return errors.New("expected string in resource list")
		}
	}
	return nil
}

func (r ResourceDefinition_0_3) MarshalJSON() ([]byte, error) {
	// Return a list if we can.
	var slugs []string
	for alias, slug := range r.Attachments {
		// If we have a single case of alias != slug, just return the map.
		if alias != slug {
			return json.Marshal(r.Attachments)
		}
		slugs = append(slugs, slug)
	}
	return json.Marshal(slugs)
}

// MarshalYAML adds custom logic for marshaling a resource definition into YAML. There seems to be a bug with the
// go-yaml package and marshaling maps using MarshalJSON, which is why we need to include MarshalYAML as well
// (even though we useJSONMarshaler above). If we rely solely on MarshalJSON, it will marshal the resource attachments
// at the top level, e.g.
//
// resources:
// demo: db
//
// as opposed to the correct YAML:
//
// resources:
//
//	demo: db
func (r ResourceDefinition_0_3) MarshalYAML() (interface{}, error) {
	// Return a list if we can.
	var slugs []string
	for alias, slug := range r.Attachments {
		// If we have a single case of alias != slug, just return the map.
		if alias != slug {
			return r.Attachments, nil
		}
		slugs = append(slugs, slug)
	}
	return slugs, nil
}

func (r ResourceDefinition_0_3) IsZero() bool {
	return len(r.Attachments) == 0
}

type resourceCollection struct {
	bySlug map[string]string
	byName map[string]string
}

func getResourceIDsBySlugAndName(ctx context.Context, client api.IAPIClient) (resourceCollection, error) {
	collection := resourceCollection{
		bySlug: map[string]string{},
		byName: map[string]string{},
	}

	resp, err := client.ListResourceMetadata(ctx)
	if err != nil {
		return resourceCollection{}, errors.Wrap(err, "fetching resources")
	}
	for _, metadata := range resp.Resources {
		collection.bySlug[metadata.Slug] = metadata.ID
		if metadata.DefaultEnvResource != nil {
			collection.byName[metadata.DefaultEnvResource.Name] = metadata.ID
		}
	}
	return collection, nil
}

func getResourceSlugsByID(ctx context.Context, client api.IAPIClient) (map[string]string, error) {
	resp, err := client.ListResourceMetadata(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "fetching resources")
	}
	resourceSlugsByID := map[string]string{}
	for _, resource := range resp.Resources {
		resourceSlugsByID[resource.ID] = resource.Slug
	}
	return resourceSlugsByID, nil
}
