package config

// ParseContract and LoadContract are Parse and Load without the
// not-supported-yet check, so tests can exercise the whole published
// contract.
func ParseContract(file string, data []byte) (*Config, error) { return parseFile(file, data, false) }

func LoadContract(path string) (*Config, error) { return load(path, false) }
