package wirejson

import (
	"encoding/json"
	"github.com/phreddrick/GB/go/internal/racegen"
	"strings"
	"testing"
)

func TestStrictSpec(t *testing.T) {
	b, e := json.Marshal(racegen.Default())
	if e != nil {
		t.Fatal(e)
	}
	valid := string(b)
	cases := []string{`null`, valid + `{}`, strings.Replace(valid, `"name":`, `"extra":0,"name":`, 1), strings.Replace(valid, `"name":`, `"name":"duplicate","name":`, 1), strings.Replace(valid, `"name":`, `"Name":`, 1), strings.Replace(valid, `"race_type":0`, `"race_type":null`, 1), strings.Replace(valid, `"race_type":0`, `"race_type":0.5`, 1), strings.Replace(valid, `"race_type":0`, `"race_type":1e999`, 1), strings.Replace(valid, `"homeworld":0,`, ``, 1), strings.Replace(valid, `[0,0,0,0,0,0,0,100]`, `[0,0,0,0,0,0,0,100,99]`, 1), strings.Replace(valid, `[0,0,0,0,0,0,0,100]`, `[0]`, 1), strings.Replace(valid, `"iq":150`, `"iq":150,"iq":150`, 1), strings.Replace(valid, `"iq":150`, `"iq":"secret"`, 1), strings.Replace(valid, `"iq":150`, `"iq":null`, 1)}
	var spec racegen.Spec
	if Decode(b, &spec) != nil {
		t.Fatal("valid spec")
	}
	for i, c := range cases {
		if c == valid {
			t.Fatalf("case %d did not change input", i)
		}
		if e := Decode([]byte(c), &spec); e != ErrInvalid {
			t.Errorf("case %d accepted", i)
		}
	}
	if Decode([]byte("\xff"), &spec) == nil {
		t.Fatal("invalid UTF8")
	}
}
