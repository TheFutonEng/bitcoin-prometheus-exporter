package main

import (
	"reflect"
	"testing"
)

func TestResolveCollectors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config
		want    []string
		wantErr bool
	}{
		{
			name: "defaults leave wallet off",
			cfg:  config{},
			want: []string{"chain", "fees", "mempool", "mining", "network", "peers"},
		},
		{
			name: "all turns on every collector",
			cfg:  config{collectorSet: "all"},
			want: []string{"chain", "fees", "mempool", "mining", "network", "peers", "wallet"},
		},
		{
			name: "explicit set replaces the defaults",
			cfg:  config{collectorSet: "chain, mempool"},
			want: []string{"chain", "mempool"},
		},
		{
			name: "enable adds to the defaults",
			cfg:  config{collectorEnable: "wallet"},
			want: []string{"chain", "fees", "mempool", "mining", "network", "peers", "wallet"},
		},
		{
			name: "disable removes from the defaults",
			cfg:  config{collectorDisable: "peers,fees"},
			want: []string{"chain", "mempool", "mining", "network"},
		},
		{
			name: "disable wins over enable",
			cfg:  config{collectorEnable: "wallet", collectorDisable: "wallet"},
			want: []string{"chain", "fees", "mempool", "mining", "network", "peers"},
		},
		{
			name: "none plus enable builds a minimal set",
			cfg:  config{collectorSet: "none", collectorEnable: "chain"},
			want: []string{"chain"},
		},
		{
			name:    "none on its own is an error",
			cfg:     config{collectorSet: "none"},
			wantErr: true,
		},
		{
			name:    "unknown name in the set",
			cfg:     config{collectorSet: "chain,bogus"},
			wantErr: true,
		},
		{
			name:    "unknown name to enable",
			cfg:     config{collectorEnable: "bogus"},
			wantErr: true,
		},
		{
			name:    "unknown name to disable",
			cfg:     config{collectorDisable: "bogus"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCollectors(&tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveCollectors() = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCollectors: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolveCollectors() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseIntList(t *testing.T) {
	got, err := parseIntList(" 1, 6 ,144,")
	if err != nil {
		t.Fatalf("parseIntList: %v", err)
	}
	if want := []int{1, 6, 144}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parseIntList() = %v, want %v", got, want)
	}
	if _, err := parseIntList("1,soon"); err == nil {
		t.Fatal("want an error for a non-numeric target")
	}
}

func TestNewLoggerRejectsBadInput(t *testing.T) {
	if _, err := newLogger("shout", "logfmt"); err == nil {
		t.Error("want an error for an unknown log level")
	}
	if _, err := newLogger("info", "yaml"); err == nil {
		t.Error("want an error for an unknown log format")
	}
	if _, err := newLogger("debug", "json"); err != nil {
		t.Errorf("newLogger(debug, json): %v", err)
	}
}
