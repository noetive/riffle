package main

import (
	"context"
	"fmt"
	"time"

	"github.com/noetive/riffle/internal/chromium"
)

// doctor checks the machine can run Riffle and says what to fix if not.
func doctor(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	bin, err := chromium.FindChrome()
	if err != nil {
		return fmt.Errorf("no browser found: install Chrome or Chromium, or point RIFFLE_CHROME at the binary: %w", err)
	}
	fmt.Println("browser   ", bin)

	b, err := chromium.Launch(ctx)
	if err != nil {
		return fmt.Errorf("the browser would not start: %w", err)
	}
	defer b.Close()
	page, err := b.NewPage(ctx, 1280, 800)
	if err != nil {
		return fmt.Errorf("the browser started but would not open a page: %w", err)
	}
	fmt.Println("start      ok")

	got, err := page.Eval(ctx, "[1,2,3].map(x => x * 2).join('-')")
	if err != nil {
		return fmt.Errorf("page scripts did not run: %w", err)
	}
	if got != "2-4-6" {
		return fmt.Errorf("page scripts ran but returned %q, want 2-4-6", got)
	}
	fmt.Println("scripts    ok")
	fmt.Println("riffle", version, "is ready")
	return nil
}
