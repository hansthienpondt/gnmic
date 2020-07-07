package file

import (
	"bytes"
	"encoding/json"
	"log"
	"os"

	"github.com/karimra/gnmic/outputs"
	"github.com/mitchellh/mapstructure"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	outputs.Register("file", func() outputs.Output {
		return &File{
			Cfg: &Config{},
			metrics: []prometheus.Collector{
				NumberOfWrittenBytes,
				NumberOfReceivedMsgs,
				NumberOfWrittenMsgs,
			},
		}
	})
}

// File //
type File struct {
	Cfg      *Config
	file     *os.File
	logger   *log.Logger
	metrics  []prometheus.Collector
	stopChan chan struct{}
}

// Config //
type Config struct {
	FileName string `mapstructure:"filename,omitempty"`
	FileType string `mapstructure:"file-type,omitempty"`
}

func (f *File) String() string {
	b, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return string(b)
}

// Init //
func (f *File) Init(cfg map[string]interface{}, logger *log.Logger) error {
	c := new(Config)
	err := mapstructure.Decode(cfg, c)
	if err != nil {
		return err
	}
	f.Cfg = c

	var file *os.File
	switch f.Cfg.FileType {
	case "stdout":
		file = os.Stdout
	case "stderr":
		file = os.Stderr
	default:
		file, err = os.OpenFile(f.Cfg.FileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return err
		}
	}
	f.file = file
	f.logger = log.New(os.Stderr, "file_output ", log.LstdFlags|log.Lmicroseconds)
	if logger != nil {
		f.logger.SetOutput(logger.Writer())
		f.logger.SetFlags(logger.Flags())
	}
	f.stopChan = make(chan struct{})
	f.logger.Printf("initialized file output: %s", f.String())
	return nil
}

// Write //
func (f *File) Write(b []byte, meta outputs.Meta) {
	NumberOfReceivedMsgs.WithLabelValues(f.file.Name()).Inc()
	if f.Cfg.FileType == "stdout" || f.Cfg.FileType == "stderr" {
		dst := new(bytes.Buffer)
		if format, ok := meta["format"]; ok {
			if format != "textproto" {
				err := json.Indent(dst, b, "", "  ")
				if err != nil {
					f.logger.Printf("failed to write to '%s': %v", f.Cfg.FileType, err)
					return
				}
				b = dst.Bytes()
			}
		}
	}
	n, err := f.file.Write(append(b, []byte("\n")...))
	if err != nil {
		f.logger.Printf("failed to write to file '%s': %v", f.file.Name(), err)
		return
	}
	NumberOfWrittenBytes.WithLabelValues(f.file.Name()).Add(float64(n))
	NumberOfWrittenMsgs.WithLabelValues(f.file.Name()).Inc()
}

// Close //
func (f *File) Close() error {
	f.logger.Printf("closing file '%s' output", f.file.Name())
	close(f.stopChan)
	return nil
}

// Metrics //
func (f *File) Metrics() []prometheus.Collector { return f.metrics }
