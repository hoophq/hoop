package openapi

type AgentRequest struct {
	Name string `json:"name" binding:"required" example:"default"`
	Mode string `json:"mode" example:"standard"`
}

type AgentCreateResponse struct {
	Token string `json:"token" example:"grpcs://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard"`
}

type AgentListResponse struct {
	ID       string            `json:"id" format:"uuid" example:"8a4239fa-5116-4bbb-ad3c-ea1f294aac4a"`
	Token    string            `json:"token" example:"grpcs://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard"`
	Name     string            `json:"name" example:"default"`
	Mode     string            `json:"mode" example:"standard"`
	Status   string            `json:"status" example:"DISCONNECTED"`
	Metadata map[string]string `json:"metadata"`
}
