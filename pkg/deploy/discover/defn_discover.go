package discover

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/airplanedev/lib/pkg/api"
	"github.com/airplanedev/lib/pkg/deploy/taskdir"
	"github.com/airplanedev/lib/pkg/deploy/taskdir/definitions"
	"github.com/airplanedev/lib/pkg/utils/fsx"
	"github.com/airplanedev/lib/pkg/utils/logger"
	"github.com/pkg/errors"
)

type DefnDiscoverer struct {
	Client api.IAPIClient
	Logger logger.Logger

	// MissingTaskHandler is called from `GetTaskConfig` if a task ID cannot be found for a definition
	// file. The handler should either create the task and return the created task's TaskMetadata, or
	// it should return `nil` to signal that the definition should be ignored. If not set, these
	// definitions are ignored.
	MissingTaskHandler func(context.Context, definitions.DefinitionInterface) (*api.TaskMetadata, error)
}

var _ TaskDiscoverer = &DefnDiscoverer{}

func (dd *DefnDiscoverer) GetAirplaneTasks(ctx context.Context, file string) ([]string, error) {
	if !definitions.IsTaskDef(file) {
		return nil, nil
	}

	dir, err := taskdir.Open(file)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	def, err := dir.ReadDefinition()
	if err != nil {
		return nil, err
	}

	return []string{def.GetSlug()}, nil
}

func (dd *DefnDiscoverer) GetTaskConfigs(ctx context.Context, file string) ([]TaskConfig, error) {
	if !definitions.IsTaskDef(file) {
		// Check if there is a file in the same directory with the same name that is a task defn.
		fileWithoutExtension := strings.TrimSuffix(file, filepath.Ext(file))
		for _, tde := range definitions.TaskDefExtensions {
			fileWithTaskDefExtension := fileWithoutExtension + tde
			if fsx.Exists(fileWithTaskDefExtension) {
				return dd.GetTaskConfigs(ctx, fileWithTaskDefExtension)
			}
		}
		return nil, nil
	}

	dir, err := taskdir.Open(file)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	def, err := dir.ReadDefinition()
	if err != nil {
		return nil, err
	}

	if err := def.Normalize(ctx, dd.Client); err != nil {
		return nil, err
	}

	tc := TaskConfig{
		Def:    def,
		Source: dd.ConfigSource(),
	}

	metadata, err := dd.Client.GetTaskMetadata(ctx, def.GetSlug())
	if err != nil {
		var merr *api.TaskMissingError
		if !errors.As(err, &merr) {
			return nil, errors.Wrap(err, "unable to get task metadata")
		}

		if dd.MissingTaskHandler == nil {
			return nil, nil
		}

		mptr, err := dd.MissingTaskHandler(ctx, def)
		if err != nil {
			return nil, err
		} else if mptr == nil {
			if dd.Logger != nil {
				dd.Logger.Warning(`Task with slug %s does not exist, skipping deployment.`, def.GetSlug())
			}
			return nil, nil
		}
		metadata = *mptr
	}
	if metadata.IsArchived {
		dd.Logger.Warning(`Task with slug %s is archived, skipping deployment.`, metadata.Slug)
		return nil, nil
	}
	tc.TaskID = metadata.ID

	entrypoint, err := def.GetAbsoluteEntrypoint()
	if err == definitions.ErrNoEntrypoint {
		return []TaskConfig{tc}, nil
	} else if err != nil {
		return nil, err
	} else if err = fsx.AssertExistsAll(entrypoint); err != nil {
		return nil, err
	} else {
		tc.TaskEntrypoint = entrypoint

		kind, _, err := def.GetKindAndOptions()
		if err != nil {
			return nil, err
		}

		taskPathMetadata, err := taskPathMetadata(entrypoint, kind)
		if err != nil {
			return nil, err
		}
		tc.TaskRoot = taskPathMetadata.RootDir
		if err := def.SetWorkdir(taskPathMetadata.RootDir, taskPathMetadata.WorkDir); err != nil {
			return nil, err
		}

		// Entrypoint for builder needs to be relative to taskroot, not definition directory.
		defnDir := filepath.Dir(dir.DefinitionPath())
		if defnDir != taskPathMetadata.RootDir {
			ep, err := filepath.Rel(taskPathMetadata.RootDir, entrypoint)
			if err != nil {
				return nil, err
			}
			def.SetBuildConfig("entrypoint", ep)
		}
	}

	return []TaskConfig{tc}, nil
}

func (dd *DefnDiscoverer) ConfigSource() ConfigSource {
	return ConfigSourceDefn
}
