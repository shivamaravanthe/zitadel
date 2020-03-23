package config

import (
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

type test struct {
	Test bool
}

type validatable struct {
	Test bool
}

func (v *validatable) Validate() error {
	if v.Test {
		return nil
	}
	return errors.New("invalid")
}

func TestRead(t *testing.T) {
	type args struct {
		obj         interface{}
		configFiles []string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			"not supoorted config file error",
			args{
				configFiles: []string{"notsupported.unknown"},
				obj:         nil,
			},
			true,
		},
		{
			"non existing config file error",
			args{
				configFiles: []string{"nonexisting.yaml"},
				obj:         nil,
			},
			true,
		},
		{
			"non parsable config file error",
			args{
				configFiles: []string{"./testdata/non_parsable.json"},
				obj:         &test{},
			},
			true,
		},
		{
			"invalid parsable config file error",
			args{
				configFiles: []string{"./testdata/invalid.json"},
				obj:         &validatable{},
			},
			true,
		},
		{
			"parsable config file ok",
			args{
				configFiles: []string{"./testdata/valid.json"},
				obj:         &test{},
			},
			false,
		},
		{
			"valid parsable config file ok",
			args{
				configFiles: []string{"./testdata/valid.json"},
				obj:         &validatable{},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Read(tt.args.obj, tt.args.configFiles...); (err != nil) != tt.wantErr {
				t.Errorf("Read() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_readerFuncForFile(t *testing.T) {
	type args struct {
		configFile string
	}
	tests := []struct {
		name    string
		args    args
		want    ReaderFunc
		wantErr bool
	}{
		{
			"unknown extension error",
			args{configFile: "test.unknown"},
			nil,
			true,
		},
		{
			"toml",
			args{configFile: "test.toml"},
			TOMLReader,
			false,
		},
		{
			"json",
			args{configFile: "test.json"},
			JSONReader,
			false,
		},
		{
			"yaml",
			args{configFile: "test.yaml"},
			YAMLReader,
			false,
		},
		{
			"yml",
			args{configFile: "test.yml"},
			YAMLReader,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readerFuncForFile(tt.args.configFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("configReaderForFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			funcName1 := runtime.FuncForPC(reflect.ValueOf(got).Pointer()).Name()
			funcName2 := runtime.FuncForPC(reflect.ValueOf(tt.want).Pointer()).Name()
			if !assert.Equal(t, funcName1, funcName2) {
				t.Errorf("configReaderForFile() got = %v, want %v", funcName1, funcName2)
			}
		})
	}
}

func Test_readConfigFile(t *testing.T) {
	type args struct {
		configReader ReaderFunc
		configFile   string
		obj          interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			"non existing config file error",
			args{
				configReader: YAMLReader,
				configFile:   "nonexisting.json",
				obj:          nil,
			},
			true,
		},
		{
			"non parsable config file error",
			args{
				configReader: YAMLReader,
				configFile:   "./testdata/non_parsable.json",
				obj:          &test{},
			},
			true,
		},
		{
			"parsable config file no error",
			args{
				configReader: YAMLReader,
				configFile:   "./testdata/valid.json",
				obj:          &test{},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := readConfigFile(tt.args.configReader, tt.args.configFile, tt.args.obj); (err != nil) != tt.wantErr {
				t.Errorf("readConfigFile() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
