package xui

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Тексты ошибок панели 3x-ui. Все клиентские эндпоинты панели отвечают
// HTTP 200 и конвертом {success, msg, obj}; структурных кодов ошибок нет,
// поэтому классификация опирается на подстроки msg. Тексты зафиксированы
// в doc/panel-api-contract.md (раздел 3, проверено на живой панели).
const (
	// del: Something went wrong (client "<email>" not found in any inbound or client record\n)
	msgNotFoundInAnyInbound = "not found in any inbound"
	// update/resetTraffic: Something went wrong (record not found)
	// get: Obtain (record not found)
	msgRecordNotFound = "record not found"
	// traffic (legacy поведение): success=false и msg содержит "client not found".
	msgClientNotFoundMsg = "client not found"
	// add: email already in use: <email> / subId already in use: <subId>
	msgAlreadyInUse = "already in use"
	// add: обобщённый дубликат (ветка контракта)
	msgDuplicate = "duplicate"
)

// panelOp — операция панели, к которой относится ответ.
type panelOp string

const (
	opAdd          panelOp = "add"
	opUpdate       panelOp = "update"
	opDel          panelOp = "del"
	opResetTraffic panelOp = "resetTraffic"
	opTraffic      panelOp = "traffic"
	opGet          panelOp = "get"
)

// classifyFailure сопоставляет msg неуспешного ответа панели с sentinel-ошибкой.
// Сопоставление регистронезависимое; распознаются только тексты из контракта.
// Возвращает nil, если текст не распознан (вызывающий код вернёт общую ошибку).
func classifyFailure(op panelOp, msg string) error {
	m := strings.ToLower(msg)

	switch op {
	case opAdd:
		if strings.Contains(m, msgAlreadyInUse) || strings.Contains(m, msgDuplicate) {
			return fmt.Errorf("%w: %s", ErrClientAlreadyExists, msg)
		}
	case opUpdate, opResetTraffic, opGet:
		if strings.Contains(m, msgRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrClientNotFound, msg)
		}
	case opDel:
		if strings.Contains(m, msgNotFoundInAnyInbound) {
			return fmt.Errorf("%w: %s", ErrClientNotFound, msg)
		}
	case opTraffic:
		if strings.Contains(m, msgClientNotFoundMsg) {
			return fmt.Errorf("%w: %s", ErrClientNotFound, msg)
		}
	}

	return nil
}

// parseEnvelope разбирает конверт {success, msg, obj} ответа панели и
// классифицирует неуспешные ответы в типизированные ошибки. Возвращает obj
// успешного ответа.
func parseEnvelope(op panelOp, respBody []byte) (json.RawMessage, error) {
	var apiResp APIResponse

	err := json.Unmarshal(respBody, &apiResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s response: %w", op, err)
	}

	if !apiResp.Success {
		classErr := classifyFailure(op, apiResp.Msg)
		if classErr != nil {
			return nil, classErr
		}

		return nil, fmt.Errorf("%s failed: %s", op, apiResp.Msg)
	}

	return apiResp.Obj, nil
}

// isNullObj сообщает, что панель вернула success=true с пустым obj
// (json null или пустое тело).
func isNullObj(obj json.RawMessage) bool {
	return len(obj) == 0 || string(obj) == "null"
}
