package actions

import (
	"log"

	"github.com/karimra/gnmic/formatters"
	"github.com/mitchellh/mapstructure"
)

type Action interface {
	Init(map[string]interface{}, ...Option) error
	Run(*formatters.EventMsg) (interface{}, error)

	WithTargets(map[string]interface{})
	WithLogger(*log.Logger)
}

type Option func(Action)

var Actions = map[string]Initializer{}

type Initializer func() Action

func Register(name string, initFn Initializer) {
	Actions[name] = initFn
}

func DecodeConfig(src, dst interface{}) error {
	decoder, err := mapstructure.NewDecoder(
		&mapstructure.DecoderConfig{
			DecodeHook: mapstructure.StringToTimeDurationHookFunc(),
			Result:     dst,
		},
	)
	if err != nil {
		return err
	}
	return decoder.Decode(src)
}

func WithTargets(tcs map[string]interface{}) Option {
	return func(a Action) {
		a.WithTargets(tcs)
	}
}

func WithLogger(l *log.Logger) Option {
	return func(a Action) {
		a.WithLogger(l)
	}
}
