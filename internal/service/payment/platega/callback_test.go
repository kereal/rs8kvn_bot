package platega

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCallbackAmount(t *testing.T) {
	tests := []struct {
		name    string
		amount  json.Number
		want    int64
		wantErr bool
	}{
		{name: "whole", amount: "230", want: 23000},
		{name: "fraction", amount: "230.5", want: 23050},
		{name: "too precise", amount: "230.001", wantErr: true},
		{name: "exponent", amount: "2.3e2", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCallbackAmount(tt.amount)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestVerifyHeaders(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Merchantid", "merchant")
	headers.Set("X-Secret", "secret")
	require.True(t, VerifyHeaders("merchant", "secret", headers))
	require.False(t, VerifyHeaders("merchant", "wrong", headers))
	require.False(t, VerifyHeaders("wrong", "secret", headers))
}
