package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestApplySkillInstructionPreservesNewerEdits(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	for _, field := range []string{"instruction", "metadata", "name", "description", "category", "reasoning", "deleted"} {
		t.Run(field, func(t *testing.T) {
			initial := Skill{ID: "apply-" + uniq(), Name: "Original", Instruction: "Evaluated instruction", Metadata: JSON(`{"status":"active","note":"old"}`)}
			if err := st.UpsertSkill(ctx, initial); err != nil {
				t.Fatal(err)
			}
			snapshot, err := st.GetSkill(ctx, initial.ID)
			if err != nil {
				t.Fatal(err)
			}
			changed := snapshot
			switch field {
			case "instruction":
				changed.Instruction = "Human edit"
			case "metadata":
				changed.Metadata = JSON(`{"status":"draft","note":"new"}`)
			case "name":
				changed.Name = "Renamed"
			case "description":
				changed.Description = "Updated"
			case "category":
				changed.Category = "Another"
			case "reasoning":
				changed.Reasoning = "high"
			}
			if field == "deleted" {
				_, err = st.Pool.Exec(ctx, `DELETE FROM skills WHERE id=$1`, initial.ID)
			} else {
				err = st.UpsertSkill(ctx, changed)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ApplySkillInstruction(ctx, snapshot, "Stale winner"); !errors.Is(err, ErrSkillChanged) {
				t.Fatalf("stale %s accepted: %v", field, err)
			}
			current, err := st.GetSkill(ctx, initial.ID)
			if field == "deleted" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("removed skill recreated: %+v %v", current, err)
				}
				return
			}
			var currentMetadata, expectedMetadata any
			if err := json.Unmarshal(current.Metadata, &currentMetadata); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(changed.Metadata, &expectedMetadata); err != nil {
				t.Fatal(err)
			}
			if err != nil || current.Instruction != changed.Instruction || !reflect.DeepEqual(currentMetadata, expectedMetadata) || current.Name != changed.Name || current.Description != changed.Description || current.Category != changed.Category || current.Reasoning != changed.Reasoning {
				t.Fatalf("newer %s lost: %+v %v", field, current, err)
			}
		})
	}
}

func TestApplySkillInstructionHasOneConcurrentWinner(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	sk := Skill{ID: "concurrent-apply-" + uniq(), Name: "Original", Instruction: "Before", Metadata: JSON(`{"status":"draft","sourceRun":"retained-source"}`)}
	if err := st.UpsertSkill(ctx, sk); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.GetSkill(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, instruction := range []string{"Winner A", "Winner B"} {
		wg.Add(1)
		go func(instruction string) {
			defer wg.Done()
			<-start
			results <- st.ApplySkillInstruction(ctx, snapshot, instruction)
		}(instruction)
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrSkillChanged) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	current, err := st.GetSkill(ctx, sk.ID)
	if err != nil || current.Instruction == "Before" || string(current.Metadata) != string(snapshot.Metadata) || current.Name != snapshot.Name {
		t.Fatalf("incorrect conditional update: %+v %v", current, err)
	}
}
