package xui

import "errors"

var (
	// ErrClientNotFound сигнализирует, что панель не нашла клиента по email.
	// Порождается классификацией ответов add/update/del/resetTraffic/traffic/get
	// (см. classifyPanelError); тексты сообщений панели зафиксированы в
	// doc/panel-api-contract.md.
	ErrClientNotFound = errors.New("client not found")

	// ErrClientAlreadyExists сигнализирует, что панель отклонила создание
	// клиента из-за занятого email или subId (add: "already in use").
	ErrClientAlreadyExists = errors.New("client already exists")
)
