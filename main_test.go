package main

import (
	"reflect"
	"testing"
)

func TestReorderArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "combined short flags",
			args: []string{"crush", "-c", "-f", "gz", "file.txt", "-tkv"},
			want: []string{"crush", "-c", "-f", "gz", "-t", "-k", "-v", "file.txt"},
		},
		{
			name: "flags after positional args",
			args: []string{"crush", "-c", "-f", "7z", "file.txt", "-k", "-v"},
			want: []string{"crush", "-c", "-f", "7z", "-k", "-v", "file.txt"},
		},
		{
			name: "no args",
			args: []string{"crush"},
			want: []string{"crush"},
		},
		{
			name: "no flags",
			args: []string{"crush", "file.txt"},
			want: []string{"crush", "file.txt"},
		},
		{
			name: "combined with single flag",
			args: []string{"crush", "-c", "-f", "gz", "file.txt", "-tv"},
			want: []string{"crush", "-c", "-f", "gz", "-t", "-v", "file.txt"},
		},
		{
			name: "long flags untouched",
			args: []string{"crush", "--force", "file.txt"},
			want: []string{"crush", "--force", "file.txt"},
		},
		{
			name: "flag with value preserved",
			args: []string{"crush", "-f", "gz", "-o", "/tmp/out", "file.txt"},
			want: []string{"crush", "-f", "gz", "-o", "/tmp/out", "file.txt"},
		},
		{
			name: "split flag with value",
			args: []string{"crush", "-c", "-f", "gz", "-s", "10", "file.txt"},
			want: []string{"crush", "-c", "-f", "gz", "-s", "10", "file.txt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reorderArgs(tt.args)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("reorderArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}
