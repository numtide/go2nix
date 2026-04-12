package embed

import _ "embed"

//go:embed schema.json
var schema string

func Schema() string { return schema }
