package hydrators

import (
	"github.com/mitchellh/mapstructure"

	"github.com/dadrus/heimdall/internal/endpoint"
	"github.com/dadrus/heimdall/internal/pipeline/template"
)

func decodeConfig(input any, output any) error {
	dec, err := mapstructure.NewDecoder(
		&mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				endpoint.DecodeAuthenticationStrategyHookFunc(),
				template.DecodeTemplateHookFunc(),
			),
			Result:      output,
			ErrorUnused: true,
		})
	if err != nil {
		return err
	}

	return dec.Decode(input)
}
