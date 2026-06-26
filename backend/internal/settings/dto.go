package settings

// SettingDTO is one row of the settings API: the resolved current value, the
// config default, and the type + unit metadata the FE uses to render the right
// input. value is the DB override when present, otherwise default.
type SettingDTO struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Default string `json:"default"`
	Type    string `json:"type"`
	Unit    string `json:"unit"`
}

// KeyValue is one requested update in a Set/SetMany call: the tunable key and its
// new raw value (validated against the key's allowlist entry before it is stored).
type KeyValue struct {
	Key   string
	Value string
}
