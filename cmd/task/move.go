package main

import (
	"context"
	"fmt"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

func carryAndPlace(ctx context.Context, database *db.DB, task *db.Task, current db.TaskPlacement,
	rawTarget, dir string, force bool) error {
	result, err := executor.PlaceTask(ctx, database, task.ID, rawTarget, dir, force)
	for _, line := range result.Messages {
		fmt.Println(line)
	}
	return err
}
