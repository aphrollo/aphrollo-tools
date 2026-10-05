package config

import "github.com/aphrollo/aphrollo-tools/internal/tomlsubset"

// Legacy is an aphrollo.toml key read under its own name.
type Legacy struct {
	Name  string
	Value tomlsubset.Value
}

func (r *reader) aliasFile(path string) {}
