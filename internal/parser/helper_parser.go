package parser

import (
	"context"
	"fmt"
)

// helperParser delegates a legacy format to an external converter.
type helperParser struct {
	runner HelperRunner
}

func (p *helperParser) Extensions() []string { return []string{"doc", "ppt", "xls"} }

func (p *helperParser) Parse(fileName string, data []byte) (Result, error) {
	return p.ParseContext(context.Background(), fileName, data)
}

func (p *helperParser) ParseContext(ctx context.Context, fileName string, data []byte) (Result, error) {
	format := ExtensionOf(fileName)
	text, err := p.runner.Run(ctx, format, data)
	if err != nil {
		return Result{}, fmt.Errorf("external converter: %w", err)
	}
	return Result{Text: text}, nil
}
