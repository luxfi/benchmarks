// Copyright (C) 2019-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"bytes"
	"testing"
)

// TestWorkloadDeterminism verifies that calling each workload generator
// twice with the same N produces byte-identical transactions. This is a
// hard requirement for fair backend comparisons — a different tx graph
// per run would invalidate timing comparisons.
func TestWorkloadDeterminism(t *testing.T) {
	for _, w := range allWorkloads {
		t.Run(w.Name, func(t *testing.T) {
			a := w.Gen(64)
			b := w.Gen(64)
			if len(a) != len(b) {
				t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
			}
			for i := range a {
				if !bytes.Equal(a[i].Data, b[i].Data) {
					t.Errorf("tx %d Data differs", i)
				}
				if !bytes.Equal(a[i].Code, b[i].Code) {
					t.Errorf("tx %d Code differs", i)
				}
				if a[i].From != b[i].From {
					t.Errorf("tx %d From differs", i)
				}
				if a[i].To != b[i].To {
					t.Errorf("tx %d To differs", i)
				}
				if a[i].Nonce != b[i].Nonce {
					t.Errorf("tx %d Nonce differs", i)
				}
			}
		})
	}
}

// TestWorkloadShape checks that each workload produces non-empty bytecode
// and N=size txs.
func TestWorkloadShape(t *testing.T) {
	for _, w := range allWorkloads {
		t.Run(w.Name, func(t *testing.T) {
			txs := w.Gen(8)
			if len(txs) != 8 {
				t.Fatalf("Gen(8) produced %d txs", len(txs))
			}
			for i, tx := range txs {
				if len(tx.Code) == 0 {
					t.Errorf("tx %d has empty Code", i)
				}
				if tx.GasLimit == 0 {
					t.Errorf("tx %d has zero GasLimit", i)
				}
				if !tx.HasTo {
					t.Errorf("tx %d missing HasTo flag", i)
				}
			}
		})
	}
}

// TestPercentile verifies the percentile helper works for typical inputs.
func TestPercentile(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		p    float64
		want float64
	}{
		{0.0, 1},
		{0.5, 5.5},
		{0.95, 9.55},
		{0.99, 9.91},
		{1.0, 10},
	}
	for _, c := range cases {
		got := percentile(xs, c.p)
		if got < c.want-0.001 || got > c.want+0.001 {
			t.Errorf("percentile(p=%.2f) = %.4f, want %.4f", c.p, got, c.want)
		}
	}
}
