package schema

import (
	"reflect"
	"strings"
	"testing"
)

func lit(s string) Token { return Token{Literal: s} }

func TestParseTemplate(t *testing.T) {
	tests := []struct {
		in      string
		want    []Token
		wantErr string
	}{
		{in: "", want: nil},
		{in: "plain", want: []Token{lit("plain")}},
		{in: "$$", want: []Token{lit("$")}},
		{in: "$$$$", want: []Token{lit("$$")}},
		{in: "$HOME and $1", want: []Token{lit("$HOME and $1")}},
		{in: "trailing $", want: []Token{lit("trailing $")}},
		{in: "$", want: []Token{lit("$")}},
		{in: "$(VAR)", want: []Token{lit("$(VAR)")}},
		{in: "$${db.host}", want: []Token{lit("${db.host}")}},
		{in: "$2a$10$abc", want: []Token{lit("$2a$10$abc")}},
		{
			in:   "${db.host}",
			want: []Token{{Ref: &Ref{Raw: "db.host", Kind: RefHost, Component: "db"}}},
		},
		{
			in: "tcp://${db.host}:${db.port}/x",
			want: []Token{
				lit("tcp://"),
				{Ref: &Ref{Raw: "db.host", Kind: RefHost, Component: "db"}},
				lit(":"),
				{Ref: &Ref{Raw: "db.port", Kind: RefPort, Component: "db"}},
				lit("/x"),
			},
		},
		{
			in:   "${api.ports.admin}",
			want: []Token{{Ref: &Ref{Raw: "api.ports.admin", Kind: RefNamedPort, Component: "api", Port: "admin"}}},
		},
		{
			in: "$$${secrets.pw}",
			want: []Token{
				lit("$"),
				{Ref: &Ref{Raw: "secrets.pw", Kind: RefSecret, Secret: "pw"}},
			},
		},
		{in: "${db.host", wantErr: "unterminated reference"},
		{in: "x ${", wantErr: "unterminated reference"},
		{in: "${}", wantErr: "unknown reference ${}"},
		{in: "${HOME}", wantErr: "unknown reference ${HOME}"},
		{in: "${db}", wantErr: "unknown reference ${db}"},
		{in: "${db.url}", wantErr: "unknown reference ${db.url}"},
		{in: "${.host}", wantErr: "unknown reference"},
		{in: "${secrets.}", wantErr: "unknown reference"},
		{in: "${secrets.a.b}", wantErr: "unknown reference"},
		{in: "${db.ports.}", wantErr: "unknown reference"},
		{in: "${db.ports}", wantErr: "unknown reference"},
		{in: "${app.url}", wantErr: "unknown reference"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseTemplate(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %s, want %s", dump(got), dump(tt.want))
			}
		})
	}
}

func dump(tokens []Token) string {
	var parts []string
	for _, t := range tokens {
		if t.Ref != nil {
			parts = append(parts, "ref("+t.Ref.Raw+")")
		} else {
			parts = append(parts, "lit("+t.Literal+")")
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}
