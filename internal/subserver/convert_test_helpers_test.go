package subserver

import (
	"encoding/json"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

func ConvertJSONToShareLinks(body []byte) ([]string, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		logger.Error("Failed to unmarshal subscription JSON",
			zap.Error(err),
			zap.String("body_preview", utils.TruncateString(string(body), 200)))
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	var items []json.RawMessage
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			rawItem, err := json.Marshal(item)
			if err != nil {
				logger.Error("Failed to marshal JSON array item",
					zap.Error(err),
					zap.String("item_preview", utils.TruncateString(fmt.Sprintf("%v", item), 200)))
				return nil, fmt.Errorf("marshal JSON array item: %w", err)
			}
			items = append(items, rawItem)
		}
	case map[string]any:
		rawMarshalled, err := json.Marshal(v)
		if err != nil {
			logger.Error("Failed to marshal JSON object",
				zap.Error(err),
				zap.String("object_preview", utils.TruncateString(fmt.Sprintf("%v", v), 200)))
			return nil, fmt.Errorf("marshal JSON object: %w", err)
		}
		items = append(items, rawMarshalled)
	default:
		logger.Error("Unexpected JSON type in subscription body",
			zap.String("type", fmt.Sprintf("%T", raw)),
			zap.String("body_preview", utils.TruncateString(string(body), 200)))
		return nil, fmt.Errorf("unexpected JSON type: %T", raw)
	}

	var links []string
	for _, item := range items {
		link, err := ConvertSingleJSONToLink(item)
		if err != nil {
			continue
		}
		links = append(links, link)
	}
	return links, nil
}
