package operator

import (
	"context"
	"fmt"
	"strings"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/gc"
)

func deletePrompt(w core.Workspace, unsaved []string) string {
	title := w.Title
	if title == "" {
		title = w.Slug
	}
	prompt := fmt.Sprintf("Delete %q (%s)?", title, w.Slug)
	if len(unsaved) > 0 {
		prompt += " Unsaved: " + strings.Join(unsaved, ", ")
	}
	return prompt
}

func Delete(ctx context.Context, slug string, confirm func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w, err := find(slug, wantAny)
	if err != nil {
		return err
	}
	if confirm == nil {
		return fmt.Errorf("delete requires confirmation")
	}
	if err := confirm(deletePrompt(w, gc.Unsaved(w))); err != nil {
		return err
	}
	if err := gc.Delete(ctx, w); err != nil {
		return err
	}
	agent.RefreshAttention()
	return nil
}
