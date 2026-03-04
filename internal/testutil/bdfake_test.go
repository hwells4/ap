package testutil

import (
	"testing"
)

func TestBDFake_ReadyCount(t *testing.T) {
	t.Parallel()
	f := NewBDFake(
		Bead{ID: "1", Subject: "task 1", Status: "open"},
		Bead{ID: "2", Subject: "task 2", Status: "open"},
		Bead{ID: "3", Subject: "task 3", Status: "in_progress"},
	)

	if got := f.ReadyCount(); got != 2 {
		t.Errorf("ReadyCount() = %d, want 2", got)
	}
}

func TestBDFake_Claim(t *testing.T) {
	t.Parallel()
	f := NewBDFake(
		Bead{ID: "1", Subject: "task 1", Status: "open"},
		Bead{ID: "2", Subject: "task 2", Status: "in_progress"},
	)

	if ok := f.Claim("1"); !ok {
		t.Error("Claim(1) = false, want true")
	}
	if got := f.Get("1"); got.Status != "in_progress" {
		t.Errorf("after Claim(1): Status = %q, want %q", got.Status, "in_progress")
	}

	// Cannot claim already in-progress bead.
	if ok := f.Claim("2"); ok {
		t.Error("Claim(2) = true, want false (already in_progress)")
	}

	// Cannot claim non-existent bead.
	if ok := f.Claim("99"); ok {
		t.Error("Claim(99) = true, want false (not found)")
	}
}

func TestBDFake_Done(t *testing.T) {
	t.Parallel()
	f := NewBDFake(
		Bead{ID: "1", Subject: "task 1", Status: "in_progress"},
		Bead{ID: "2", Subject: "task 2", Status: "open"},
	)

	if ok := f.Done("1"); !ok {
		t.Error("Done(1) = false, want true")
	}
	if got := f.Get("1"); got.Status != "done" {
		t.Errorf("after Done(1): Status = %q, want %q", got.Status, "done")
	}

	// Cannot done an open bead.
	if ok := f.Done("2"); ok {
		t.Error("Done(2) = true, want false (not in_progress)")
	}
}

func TestBDFake_Get(t *testing.T) {
	t.Parallel()
	f := NewBDFake(Bead{ID: "1", Subject: "task 1", Status: "open"})

	got := f.Get("1")
	if got == nil {
		t.Fatal("Get(1) = nil, want bead")
	}
	if got.Subject != "task 1" {
		t.Errorf("Get(1).Subject = %q, want %q", got.Subject, "task 1")
	}

	// Returned bead is a copy — modifying it doesn't affect the fake.
	got.Status = "modified"
	original := f.Get("1")
	if original.Status != "open" {
		t.Error("Get() should return a copy, not a reference")
	}

	// Non-existent.
	if f.Get("99") != nil {
		t.Error("Get(99) should return nil")
	}
}

func TestBDFake_List(t *testing.T) {
	t.Parallel()
	f := NewBDFake(
		Bead{ID: "1", Subject: "task 1", Status: "open"},
		Bead{ID: "2", Subject: "task 2", Status: "done"},
	)

	list := f.List()
	if len(list) != 2 {
		t.Errorf("List() len = %d, want 2", len(list))
	}
}

func TestBDFake_Ready(t *testing.T) {
	t.Parallel()
	f := NewBDFake(
		Bead{ID: "1", Subject: "task 1", Status: "open"},
		Bead{ID: "2", Subject: "task 2", Status: "in_progress"},
		Bead{ID: "3", Subject: "task 3", Status: "open"},
	)

	ready := f.Ready()
	if len(ready) != 2 {
		t.Errorf("Ready() len = %d, want 2", len(ready))
	}
}

func TestBDFake_Add(t *testing.T) {
	t.Parallel()
	f := NewBDFake()

	if f.ReadyCount() != 0 {
		t.Errorf("ReadyCount() initial = %d, want 0", f.ReadyCount())
	}

	f.Add(Bead{ID: "new", Subject: "new task", Status: "open"})
	if f.ReadyCount() != 1 {
		t.Errorf("ReadyCount() after Add = %d, want 1", f.ReadyCount())
	}
}

func TestBDFake_Empty(t *testing.T) {
	t.Parallel()
	f := NewBDFake()

	if f.ReadyCount() != 0 {
		t.Errorf("ReadyCount() = %d, want 0", f.ReadyCount())
	}
	if len(f.List()) != 0 {
		t.Errorf("List() len = %d, want 0", len(f.List()))
	}
}
