package bundlediscover

import (
	"context"
	"path"
	"path/filepath"
	"testing"

	"github.com/airplanedev/lib/pkg/api"
	"github.com/airplanedev/lib/pkg/api/mock"
	"github.com/airplanedev/lib/pkg/build"
	"github.com/airplanedev/lib/pkg/deploy/discover"
	"github.com/airplanedev/lib/pkg/utils/logger"
	"github.com/airplanedev/lib/pkg/utils/pointers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscover(t *testing.T) {
	fixturesPath, _ := filepath.Abs("./fixtures")
	testCases := []struct {
		desc            string
		paths           []string
		existingTasks   map[string]api.Task
		expectedBundles []Bundle
		expectedErr     bool
	}{
		{
			desc:  "task with comment",
			paths: []string{"./fixtures/taskWithComment"},
			existingTasks: map[string]api.Task{
				"my_task": {ID: "tsk123", Slug: "my_task", Kind: build.TaskKindNode, InterpolationMode: "handlebars"},
			},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"taskWithComment/single_task.js"},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
					},
				},
			},
		},
		{
			desc:  "tasks with defn",
			paths: []string{"./fixtures/tasksWithDefn"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn.js", "tasksWithDefn/defn.task.yaml"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
					},
				},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn2.py", "tasksWithDefn/defn2.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.PythonBuildType,
						EnvVars: map[string]build.EnvVarValue{
							"baz": {Value: pointers.String("quz")},
						},
					},
				},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn3.sh", "tasksWithDefn/defn3.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.ShellBuildType,
					},
				},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn4.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.NoneBuildType,
					},
				},
			},
		},
		{
			desc:  "inline tasks",
			paths: []string{"./fixtures/inlineTasks"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"inlineTasks/codeOnlyTask.airplane.ts"},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						Base: build.BuildBaseSlim,
					},
				},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"inlineTasks/code_only_task_airplane.py"},
					BuildContext: build.BuildContext{
						Type: build.PythonBuildType,
						Base: build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "inline tasks in their own project",
			paths: []string{"./fixtures/inlineTasksOwnProject"},
			expectedBundles: []Bundle{
				{
					RootPath:    path.Join(fixturesPath, "inlineTasksOwnProject", "nested"),
					TargetPaths: []string{"codeOnlyTask.airplane.ts"},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						Base: build.BuildBaseSlim,
					},
				},
				{
					RootPath:    path.Join(fixturesPath, "inlineTasksOwnProject", "nested"),
					TargetPaths: []string{"code_only_task_airplane.py"},
					BuildContext: build.BuildContext{
						Type: build.PythonBuildType,
						Base: build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "inline tasks with version and base set",
			paths: []string{"./fixtures/inlineTasksVersion"},
			expectedBundles: []Bundle{
				{
					RootPath:    path.Join(fixturesPath, "inlineTasksVersion"),
					TargetPaths: []string{"codeOnlyTask.airplane.ts"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode18,
						Base:    build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "inline task with env vars",
			paths: []string{"./fixtures/inlineTaskWithEnvVars"},
			expectedBundles: []Bundle{
				{
					RootPath:    path.Join(fixturesPath, "inlineTaskWithEnvVars"),
					TargetPaths: []string{"codeOnlyTask.airplane.ts"},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						EnvVars: map[string]build.EnvVarValue{
							"foo": {Value: pointers.String("bar")},
						},
						Base: build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "defn task with env vars",
			paths: []string{"./fixtures/tasksWithDefnEnvVars"},
			expectedBundles: []Bundle{
				{
					RootPath: path.Join(fixturesPath),
					TargetPaths: []string{
						"tasksWithDefnEnvVars/defn.js",
						"tasksWithDefnEnvVars/defn.task.yaml",
						// Although defn3 could be in either bundle since it does not have any env vars,
						// We want to make sure it only exists in one and only one bundle.
						"tasksWithDefnEnvVars/defn3.js",
						"tasksWithDefnEnvVars/defn3.task.yaml",
					},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
						EnvVars: map[string]build.EnvVarValue{
							"foo": {Value: pointers.String("bar")},
						},
					},
				},
				{
					RootPath: path.Join(fixturesPath),
					TargetPaths: []string{
						"tasksWithDefnEnvVars/defn2.js",
						"tasksWithDefnEnvVars/defn2.task.yaml",
					},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
						EnvVars: map[string]build.EnvVarValue{
							"foo": {Value: pointers.String("another")},
						},
					},
				},
			},
		},
		{
			desc:  "non build task (sql, rest, docker)",
			paths: []string{"./fixtures/nonbuildtask"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"nonbuildtask/defn.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.NoneBuildType,
					}},
			},
		},
		{
			desc:  "non build task nested ",
			paths: []string{"./fixtures/nonbuildtasknested"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"nonbuildtasknested/defn.task.yaml", "nonbuildtasknested/nested/rest.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.NoneBuildType,
					},
				},
				{
					RootPath:    path.Join(fixturesPath, "nonbuildtasknested/nested/nested"),
					TargetPaths: []string{"code.task.yaml", "code.ts"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode18,
					},
				},
			},
		},
		{
			desc:  "view",
			paths: []string{"./fixtures/viewWithDefn"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"viewWithDefn/defn.view.yaml"},
					BuildContext: build.BuildContext{
						Type: build.ViewBuildType,
						Base: build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "inline view",
			paths: []string{"./fixtures/viewInline"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"viewInline/myView.view.tsx"},
					BuildContext: build.BuildContext{
						Type: build.ViewBuildType,
						Base: build.BuildBaseSlim,
					}},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"viewInline/myView.view.tsx"},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						Base: build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "inline view with env vars",
			paths: []string{"./fixtures/viewInlineWithEnvVars"},
			expectedBundles: []Bundle{
				{
					RootPath:    path.Join(fixturesPath, "viewInlineWithEnvVars"),
					TargetPaths: []string{"myView.airplane.tsx"},
					BuildContext: build.BuildContext{
						Type: build.ViewBuildType,
						Base: build.BuildBaseSlim,
						EnvVars: map[string]build.EnvVarValue{
							"foo": {Value: pointers.String("bar")},
						},
					}},
				{
					RootPath:    path.Join(fixturesPath, "viewInlineWithEnvVars"),
					TargetPaths: []string{"myView.airplane.tsx"},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						Base: build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "task with defn by script name",
			paths: []string{"./fixtures/tasksWithDefn/defn.js"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn.js"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
					},
				},
			},
		},
		{
			desc:  "multiple paths",
			paths: []string{"./fixtures/inlineTasks", "./fixtures/tasksWithDefn", "./fixtures/tasksWithDefn", "./fixtures/tasksWithDefn/defn.task.yaml"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"inlineTasks/codeOnlyTask.airplane.ts"},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						Base: build.BuildBaseSlim,
					},
				},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"inlineTasks/code_only_task_airplane.py"},
					BuildContext: build.BuildContext{
						Type: build.PythonBuildType,
						Base: build.BuildBaseSlim,
					},
				},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn2.py", "tasksWithDefn/defn2.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.PythonBuildType,
						EnvVars: map[string]build.EnvVarValue{
							"baz": {Value: pointers.String("quz")},
						},
					},
				},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn.js", "tasksWithDefn/defn.task.yaml"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
					}},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn3.sh", "tasksWithDefn/defn3.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.ShellBuildType,
					}},
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"tasksWithDefn/defn4.task.yaml"},
					BuildContext: build.BuildContext{
						Type: build.NoneBuildType,
					}},
			},
		},
		{
			desc:  "task nested in a folder",
			paths: []string{"./fixtures/nestedTask"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"nestedTask/nestedFolder/defn.js", "nestedTask/nestedFolder/defn.task.yaml"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode16,
					}},
			},
		},
		{
			desc:  "multiple tasks same root same build",
			paths: []string{"./fixtures/multipleTasksSameRoot"},
			expectedBundles: []Bundle{
				{
					RootPath: fixturesPath,
					TargetPaths: []string{
						"multipleTasksSameRoot/codeOnlyTask.airplane.ts",
						"multipleTasksSameRoot/defn.js",
						"multipleTasksSameRoot/defn.task.yaml",
						"multipleTasksSameRoot/defn2.js",
						"multipleTasksSameRoot/defn2.task.yaml",
					},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						Base: build.BuildBaseSlim,
					}},
			},
		},
		{
			desc:  "multiple tasks same root diff build",
			paths: []string{"./fixtures/multipleTasksSameRootDiffBuild"},
			expectedBundles: []Bundle{
				{
					RootPath: fixturesPath,
					TargetPaths: []string{
						"multipleTasksSameRootDiffBuild/defn.js",
						"multipleTasksSameRootDiffBuild/defn.task.yaml",
					},
					BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
					}},
				{
					RootPath: fixturesPath,
					TargetPaths: []string{
						"multipleTasksSameRootDiffBuild/defn2.js",
						"multipleTasksSameRootDiffBuild/defn2.task.yaml",
					},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode16,
					}},
				{
					RootPath: fixturesPath,
					TargetPaths: []string{
						"multipleTasksSameRootDiffBuild/defn3.js",
						"multipleTasksSameRootDiffBuild/defn3.task.yaml",
					}, BuildContext: build.BuildContext{
						Type: build.NodeBuildType,
						Base: build.BuildBaseSlim,
					},
				},
			},
		},
		{
			desc:  "multiple tasks diff root",
			paths: []string{"./fixtures/multipleTasksDiffRoot"},
			expectedBundles: []Bundle{
				{
					RootPath:    fixturesPath,
					TargetPaths: []string{"multipleTasksDiffRoot/defn.js", "multipleTasksDiffRoot/defn.task.yaml"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
					}},
				{
					RootPath:    path.Join(fixturesPath, "multipleTasksDiffRoot/nested"),
					TargetPaths: []string{"defn2.js", "defn2.task.yaml"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
					}},
				{
					RootPath:    path.Join(fixturesPath, "multipleTasksDiffRoot/nested/nested"),
					TargetPaths: []string{"defn2.js", "defn2.task.yaml"},
					BuildContext: build.BuildContext{
						Type:    build.NodeBuildType,
						Version: build.BuildTypeVersionNode14,
					}},
			},
		},
		{
			desc:            "no entities match paths",
			paths:           []string{"./discover_test.go"},
			expectedBundles: []Bundle{},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			apiClient := &mock.MockClient{
				Tasks: tC.existingTasks,
			}
			scriptDiscoverer := &discover.ScriptDiscoverer{
				Client: apiClient,
				Logger: &logger.MockLogger{},
			}
			defnDiscoverer := &discover.DefnDiscoverer{
				Client: apiClient,
				Logger: &logger.MockLogger{},
			}
			codeTaskDiscoverer := &discover.CodeTaskDiscoverer{
				Client: apiClient,
				Logger: &logger.MockLogger{},
			}
			viewDefnDiscoverer := &discover.ViewDefnDiscoverer{
				Client: apiClient,
				Logger: &logger.MockLogger{},
			}
			codeViewDiscoverer := &discover.CodeViewDiscoverer{
				Client: apiClient,
				Logger: &logger.MockLogger{},
			}
			d := &Discoverer{
				TaskDiscoverers: []discover.TaskDiscoverer{defnDiscoverer, scriptDiscoverer, codeTaskDiscoverer},
				ViewDiscoverers: []discover.ViewDiscoverer{viewDefnDiscoverer, codeViewDiscoverer},
				Client:          apiClient,
				Logger:          &logger.MockLogger{},
			}

			bundles, err := d.Discover(context.Background(), tC.paths...)
			if tC.expectedErr {
				require.NotNil(err)
				return
			}
			require.NoError(err)

			assert.ElementsMatch(tC.expectedBundles, bundles)
		})
	}
}
