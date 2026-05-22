package command

import (
	"context"

	"github.com/marcomoesman/prompto/internal/attribution"
)

type LicenseCommand struct{}

func NewLicenseCommand() Command { return LicenseCommand{} }

func (LicenseCommand) Name() string { return "license" }

func (LicenseCommand) Aliases() []string { return []string{"licenses", "notice", "notices"} }

func (LicenseCommand) Kind() Kind { return KindLocal }

func (LicenseCommand) Help() string { return "show prompto and third-party license notices" }

func (LicenseCommand) Exec(_ context.Context, _ []string, _ Env) (Result, error) {
	return Result{
		Message:         attribution.RenderLicenseReport(),
		MessageMarkdown: true,
	}, nil
}
