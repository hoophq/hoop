package redactor

type DataMaskingEntityData struct {
	SupportedEntityTypes []SupportedEntityTypesEntry `json:"supported_entity_types"`
}

type SupportedEntityTypesEntry struct {
	EntityTypes []string `json:"entity_types"`
}
