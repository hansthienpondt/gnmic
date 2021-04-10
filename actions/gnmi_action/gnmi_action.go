package gnmi_action

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/karimra/gnmic/actions"
	"github.com/karimra/gnmic/formatters"
	"github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	defaultRPC      = "get"
	defaultTimeout  = 5 * time.Second
	loggingPrefix   = "[gnmi_action] "
	actionType      = "gnmi"
	defaultDataType = "ALL"
	defaultTarget   = `{{ index .Tags "source" }}`
	defaultEncoding = "JSON"
)

func init() {
	actions.Register(actionType, func() actions.Action {
		return &gnmiAction{
			logger:         log.New(ioutil.Discard, "", 0),
			targetsConfigs: make(map[string]*targetConfig),
		}
	})
}

type gnmiAction struct {
	Target string   `mapstructure:"target,omitempty"`
	RPC    string   `mapstructure:"rpc,omitempty"`
	Prefix string   `mapstructure:"prefix,omitempty"`
	Paths  []string `mapstructure:"paths,omitempty"`
	Type   string   `mapstructure:"data-type,omitempty"`

	Values []string `mapstructure:"values,omitempty"`
	//ValuesFromFiles []string `mapstructure:"values-from-files,omitempty"`
	Encoding   string `mapstructure:"encoding,omitempty"`
	Debug      bool   `mapstructure:"debug,omitempty"`
	NoEnvProxy bool   `mapstructure:"no-env-proxy,omitempty"`
	Insecure   bool   `mapstructure:"insecure,omitempty"`
	SkipVerify bool   `mapstructure:"skip-verify,omitempty"`
	TLSCa      string `mapstructure:"tls-ca,omitempty"`
	TLSCert    string `mapstructure:"tls-cert,omitempty"`
	TLSKey     string `mapstructure:"tls-key,omitempty"`

	target *template.Template
	prefix *template.Template
	paths  []*template.Template
	values []*template.Template

	targetsConfigs map[string]*targetConfig
	logger         *log.Logger
}

func (g *gnmiAction) Init(cfg map[string]interface{}, opts ...actions.Option) error {
	err := actions.DecodeConfig(cfg, g)
	if err != nil {
		return err
	}
	for _, opt := range opts {
		opt(g)
	}

	g.setDefaults()
	err = g.parseTemplates()
	if err != nil {
		return err
	}
	return g.validate()
}

func (g *gnmiAction) Run(e *formatters.EventMsg) (interface{}, error) {
	b := new(bytes.Buffer)
	err := g.target.Execute(b, e)
	if err != nil {
		return nil, err
	}
	if tc, ok := g.targetsConfigs[b.String()]; ok {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		t := newTarget(tc)
		switch g.RPC {
		case "get":
			req, err := g.createGetRequest(e)
			if err != nil {
				return nil, err
			}
			err = t.createGNMIClient(ctx, g.dialOpts()...) // TODO add dialopts
			if err != nil {
				return nil, err
			}
			return t.Get(ctx, req)
		case "set-update", "set-replace", "delete":
			req, err := g.createSetRequest(e)
			if err != nil {
				return nil, err
			}
			err = t.createGNMIClient(ctx, g.dialOpts()...) // TODO add dialopts
			if err != nil {
				return nil, err
			}
			return t.Set(ctx, req)
		}
	}
	return nil, fmt.Errorf("unknown target %q", b.String())
}

func (g *gnmiAction) setDefaults() {
	if g.Type == "" {
		g.Type = defaultDataType
	}
	if g.Encoding == "" {
		g.Encoding = defaultEncoding
	}
	if g.RPC == "" {
		g.RPC = "get"
	}
	if g.RPC == "set" {
		g.RPC = "set-update"
	}
	if g.Target == "" {
		g.Target = defaultTarget
	}
}

func (g *gnmiAction) validate() error {
	numPaths := len(g.Paths)
	if numPaths == 0 {
		return errors.New("paths field is required")
	}
	switch g.RPC {
	case "get", "delete":
	case "set-update", "set-replace":
		numValues := len(g.values)
		if numValues == 0 {
			return errors.New("values field is required when RPC is set")
		}
		if numPaths != len(g.values) {
			return errors.New("number of paths and values do not match")
		}
	default:
		return fmt.Errorf("unknown gnmi RPC %q", g.RPC)
	}
	return nil
}

func (g *gnmiAction) parseTemplates() error {
	var err error
	g.target, err = template.New("target").Parse(g.Target)
	if err != nil {
		return err
	}
	g.prefix, err = template.New("prefix").Parse(g.Prefix)
	if err != nil {
		return err
	}
	g.paths, err = g.createTemplates("path", g.Paths)
	if err != nil {
		return err
	}
	g.values, err = g.createTemplates("value", g.Values)
	return err
}

func (g *gnmiAction) createTemplates(n string, s []string) ([]*template.Template, error) {
	tpls := make([]*template.Template, 0, len(s))
	for i, p := range s {
		tpl, err := template.New(fmt.Sprintf("%s-%d", n, i)).Parse(p)
		if err != nil {
			return nil, err
		}
		tpls = append(tpls, tpl)
	}
	return tpls, nil
}

func (g *gnmiAction) createGetRequest(e *formatters.EventMsg) (*gnmi.GetRequest, error) {
	encodingVal, ok := gnmi.Encoding_value[strings.Replace(strings.ToUpper(g.Encoding), "-", "_", -1)]
	if !ok {
		return nil, fmt.Errorf("invalid encoding type '%s'", g.Encoding)
	}
	req := &gnmi.GetRequest{
		UseModels: make([]*gnmi.ModelData, 0),
		Path:      make([]*gnmi.Path, 0, len(g.paths)),
		Encoding:  gnmi.Encoding(encodingVal),
	}
	var err error
	b := new(bytes.Buffer)
	if g.Prefix != "" {
		err = g.prefix.Execute(b, e)
		if err != nil {
			return nil, fmt.Errorf("prefix parse error: %v", err)
		}
		gnmiPrefix, err := parsePath(b.String())
		if err != nil {
			return nil, fmt.Errorf("prefix parse error: %v", err)
		}
		req.Prefix = gnmiPrefix
	}
	if g.Type != "" {
		dti, ok := gnmi.GetRequest_DataType_value[strings.ToUpper(g.Type)]
		if !ok {
			return nil, fmt.Errorf("unknown data type %s", g.Type)
		}
		req.Type = gnmi.GetRequest_DataType(dti)
	}
	for _, p := range g.paths {
		b.Reset()
		err = p.Execute(b, e)
		if err != nil {
			return nil, fmt.Errorf("path parse error: %v", err)
		}
		gnmiPath, err := parsePath(strings.TrimSpace(b.String()))
		if err != nil {
			return nil, fmt.Errorf("path parse error: %v", err)
		}
		req.Path = append(req.Path, gnmiPath)
	}
	return req, nil
}

func (g *gnmiAction) createSetRequest(e *formatters.EventMsg) (*gnmi.SetRequest, error) {
	req := &gnmi.SetRequest{
		Delete:  make([]*gnmi.Path, 0, len(g.paths)),
		Replace: make([]*gnmi.Update, 0, len(g.paths)),
		Update:  make([]*gnmi.Update, 0, len(g.paths)),
	}

	var err error
	b := new(bytes.Buffer)
	if g.Prefix != "" {
		err = g.prefix.Execute(b, e)
		if err != nil {
			return nil, fmt.Errorf("prefix parse error: %v", err)
		}
		gnmiPrefix, err := parsePath(b.String())
		if err != nil {
			return nil, fmt.Errorf("prefix parse error: %v", err)
		}
		req.Prefix = gnmiPrefix
	}
	for i, p := range g.paths {
		b.Reset()
		err = p.Execute(b, e)
		if err != nil {
			return nil, fmt.Errorf("path parse error: %v", err)
		}
		gnmiPath, err := parsePath(strings.TrimSpace(b.String()))
		if err != nil {
			return nil, fmt.Errorf("path parse error: %v", err)
		}

		// value
		b.Reset()
		err = g.values[i].Execute(b, e)
		if err != nil {
			return nil, fmt.Errorf("value parse error: %v", err)
		}
		val, err := g.createTypedValue(b.String())
		if err != nil {
			return nil, fmt.Errorf("create value error: %v", err)
		}
		switch g.RPC {
		case "set-update":
			req.Update = append(req.Update, &gnmi.Update{
				Path: gnmiPath,
				Val:  val,
			})
		case "set-replace":
			req.Replace = append(req.Replace, &gnmi.Update{
				Path: gnmiPath,
				Val:  val,
			})
		}
	}
	return req, nil
}

func (g *gnmiAction) createTypedValue(val string) (*gnmi.TypedValue, error) {
	var err error
	value := new(gnmi.TypedValue)
	switch strings.ToLower(g.Encoding) {
	case "json":
		buff := new(bytes.Buffer)
		err = json.NewEncoder(buff).Encode(strings.TrimRight(strings.TrimLeft(val, "["), "]"))
		if err != nil {
			return nil, err
		}
		value.Value = &gnmi.TypedValue_JsonVal{
			JsonVal: bytes.Trim(buff.Bytes(), " \r\n\t"),
		}
	case "json_ietf":
		buff := new(bytes.Buffer)
		err = json.NewEncoder(buff).Encode(strings.TrimRight(strings.TrimLeft(val, "["), "]"))
		if err != nil {
			return nil, err
		}
		value.Value = &gnmi.TypedValue_JsonIetfVal{
			JsonIetfVal: bytes.Trim(buff.Bytes(), " \r\n\t"),
		}
	case "ascii":
		value.Value = &gnmi.TypedValue_AsciiVal{
			AsciiVal: val,
		}
	case "bool":
		bval, err := strconv.ParseBool(val)
		if err != nil {
			return nil, err
		}
		value.Value = &gnmi.TypedValue_BoolVal{
			BoolVal: bval,
		}
	case "bytes":
		value.Value = &gnmi.TypedValue_BytesVal{
			BytesVal: []byte(val),
		}
	case "decimal":
		dVal := &gnmi.Decimal64{}
		value.Value = &gnmi.TypedValue_DecimalVal{
			DecimalVal: dVal,
		}
		return nil, fmt.Errorf("decimal type not implemented")
	case "float":
		f, err := strconv.ParseFloat(val, 32)
		if err != nil {
			return nil, err
		}
		value.Value = &gnmi.TypedValue_FloatVal{
			FloatVal: float32(f),
		}
	case "int":
		k, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, err
		}
		value.Value = &gnmi.TypedValue_IntVal{
			IntVal: k,
		}
	case "uint":
		u, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return nil, err
		}
		value.Value = &gnmi.TypedValue_UintVal{
			UintVal: u,
		}
	case "string":
		value.Value = &gnmi.TypedValue_StringVal{
			StringVal: val,
		}
	default:
		return nil, fmt.Errorf("unknown type %q", g.Encoding)
	}
	return value, nil
}

func (g *gnmiAction) dialOpts() []grpc.DialOption {
	opts := make([]grpc.DialOption, 0, 3)
	opts = append(opts, grpc.WithBlock())
	if g.NoEnvProxy {
		opts = append(opts, grpc.WithNoProxy())
	}
	if g.Insecure {
		return opts
	}
	tlsConfig := &tls.Config{
		Renegotiation:      tls.RenegotiateNever,
		InsecureSkipVerify: g.SkipVerify,
	}
	err := g.loadCerts(tlsConfig)
	if err != nil {
		g.logger.Printf("failed loading certificates: %v", err)
	}

	err = g.loadCACerts(tlsConfig)
	if err != nil {
		g.logger.Printf("failed loading CA certificates: %v", err)
	}
	opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	return opts
}

func (g *gnmiAction) loadCerts(tlscfg *tls.Config) error {
	if g.TLSCert != "" && g.TLSKey != "" {
		certificate, err := tls.LoadX509KeyPair(g.TLSCert, g.TLSKey)
		if err != nil {
			return err
		}
		tlscfg.Certificates = []tls.Certificate{certificate}
		tlscfg.BuildNameToCertificate()
	}
	return nil
}

func (g *gnmiAction) loadCACerts(tlscfg *tls.Config) error {
	certPool := x509.NewCertPool()
	if g.TLSCa != "" {
		caFile, err := ioutil.ReadFile(g.TLSCa)
		if err != nil {
			return err
		}
		if ok := certPool.AppendCertsFromPEM(caFile); !ok {
			return errors.New("failed to append certificate")
		}
		tlscfg.RootCAs = certPool
	}
	return nil
}
