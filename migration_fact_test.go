package bubblelint

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"go/token"
	"reflect"
	"testing"
)

func sampleMigrationFact() *migrationFact {
	frame := migrationFrame{Symbol: "worker.Run", Position: token.Position{Filename: "worker.go", Offset: 42, Line: 5, Column: 2}}
	leaf := &migrationProgram{Steps: []migrationInstruction{{Kind: 'e', Expr: &migrationExpression{Kind: 'b', Text: "true"}}}, Size: 2}
	fact := &migrationFact{
		Reasons: []migrationReason{{Kind: effectNetwork, Message: "network I/O", Trace: []migrationFrame{frame}, Conditional: true}},
		Timer:   true,
		program: &migrationProgram{Size: 8, Steps: []migrationInstruction{{
			Kind: 'i', Expr: &migrationExpression{Kind: 'p', Input: 0},
			Then: []migrationInstruction{{Kind: 'e', Expr: &migrationExpression{
				Kind: 'c', Target: &migrationTarget{Frame: frame, Program: leaf, Callbacks: []int{0}},
			}}},
			Else: []migrationInstruction{{Kind: 'r'}},
		}}},
	}
	fact.Decision, _ = json.Marshal(fact.program)
	return fact
}

func TestMigrationFactEncoding(t *testing.T) {
	for _, original := range []*migrationFact{{}, sampleMigrationFact()} {
		var first, second bytes.Buffer
		for _, output := range []*bytes.Buffer{&first, &second} {
			if err := gob.NewEncoder(output).Encode(original); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatal("fact encoding is not deterministic")
		}
		var decoded migrationFact
		if err := gob.NewDecoder(&first).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original.Decision, decoded.Decision) || !reflect.DeepEqual(original.Reasons, decoded.Reasons) || original.Timer != decoded.Timer {
			t.Fatalf("fact changed across package boundary: got %+v, want %+v", decoded, original)
		}
		if decoded.program != nil {
			t.Fatal("propagation decoded an unused decision")
		}
		if len(decoded.Decision) != 0 {
			if err := json.Unmarshal(decoded.Decision, &decoded.program); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(original.program, decoded.program) {
			t.Fatalf("fact changed across package boundary: got %+v, want %+v", decoded, original)
		}
	}
}

func BenchmarkMigrationFactEncoding(b *testing.B) {
	// Compare propagation of a recursive decision with the encoded payload.
	type eagerFact struct {
		Reasons []migrationReason
		Timer   bool
		Program *migrationProgram
	}
	fact := sampleMigrationFact()
	for _, name := range []string{"eager", "deferred"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var buffer bytes.Buffer
				var input, output any = fact, new(migrationFact)
				if name == "eager" {
					input, output = &eagerFact{fact.Reasons, fact.Timer, fact.program}, new(eagerFact)
				}
				if err := gob.NewEncoder(&buffer).Encode(input); err != nil {
					b.Fatal(err)
				}
				if err := gob.NewDecoder(&buffer).Decode(output); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
