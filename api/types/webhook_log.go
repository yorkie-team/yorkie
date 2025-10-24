package types

import "time"

type WebhookLogInfo struct {
	ID           ID
	ProjectID    ID
	WebhookType  string // "auth" or "event"
	WebhookURL   string
	RequestBody  []byte
	StatusCode   int
	ResponseBody []byte
	ErrorMessage string
	CreatedAt    time.Time
}

// DeepCopy returns a deep copy of the WebhookLogInfo.
func (i *WebhookLogInfo) DeepCopy() *WebhookLogInfo {
	if i == nil {
		return nil
	}

	requestBody := make([]byte, len(i.RequestBody))
	copy(requestBody, i.RequestBody)

	responseBody := make([]byte, len(i.ResponseBody))
	copy(responseBody, i.ResponseBody)

	return &WebhookLogInfo{
		ID:           i.ID,
		ProjectID:    i.ProjectID,
		WebhookType:  i.WebhookType,
		WebhookURL:   i.WebhookURL,
		RequestBody:  requestBody,
		StatusCode:   i.StatusCode,
		ResponseBody: responseBody,
		ErrorMessage: i.ErrorMessage,
		CreatedAt:    i.CreatedAt,
	}
}
