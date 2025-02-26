package definitions

const viewDefinitionTemplate = `# Used by Airplane to identify your view. Do not change.
slug: {{.slug}}

# A human-readable name for your view.
name: {{.name}}

# A human-readable description for your view.
# description: "My Airplane view"

# The path to the .view.tsx file containing the logic for this view. This
# can be absolute or relative to the location of the definition file.
entrypoint: {{.entrypoint}}
`
