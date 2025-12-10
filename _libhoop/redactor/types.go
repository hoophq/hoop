package redactor

type DataMaskingEntityData struct {
	Name                 string                      `json:"name"`
	SupportedEntityTypes []SupportedEntityTypesEntry `json:"supported_entity_types"`
}

type SupportedEntityTypesEntry struct {
	Name        string   `json:"name"`
	EntityTypes []string `json:"entity_types"`
}
