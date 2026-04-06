package compile

import (
	"reflect"
	"testing"
)

func TestOptions_outputFlags(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "default mode (no IfaceOutput)",
			opts: Options{Output: "/out/foo.a"},
			want: []string{"-o", "/out/foo.a"},
		},
		{
			name: "iface split mode",
			opts: Options{Output: "/out/foo.a", IfaceOutput: "/iface/foo.x"},
			want: []string{"-o", "/iface/foo.x", "-linkobj", "/out/foo.a"},
		},
		{
			name: "empty IfaceOutput stays in default mode",
			opts: Options{Output: "/out/foo.a", IfaceOutput: ""},
			want: []string{"-o", "/out/foo.a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.opts.outputFlags()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("outputFlags() = %v, want %v", got, tt.want)
			}
		})
	}
}
