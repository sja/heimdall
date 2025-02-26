package httpendpoint

import (
	"github.com/mitchellh/mapstructure"

	"github.com/dadrus/heimdall/internal/endpoint"
)

func decodeConfig(input any, output any) error {
	dec, err := mapstructure.NewDecoder(
		&mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				endpoint.DecodeAuthenticationStrategyHookFunc(),
				mapstructure.StringToTimeDurationHookFunc(),
			),
			Result:      output,
			ErrorUnused: true,
		})
	if err != nil {
		return err
	}

	return dec.Decode(input)
}
