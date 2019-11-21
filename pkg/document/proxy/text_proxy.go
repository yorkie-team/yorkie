package proxy

import (
	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type TextProxy struct {
	*datatype.Text
	context *change.Context
}

func ProxyText(ctx *change.Context, text *datatype.Text) *TextProxy {
	return NewTextProxy(ctx, text.CreatedAt())
}

func (p *TextProxy) Edit(from, to int, content string) *TextProxy {
	ticket := p.context.IssueTimeTicket()
	p.Text.Edit(from, to, content)

	p.context.Push(operation.NewEdit(
		p.CreatedAt(),
		content,
		ticket,
	))

	return p
}

func NewTextProxy(
	ctx *change.Context,
	createdAt *time.Ticket,
) *TextProxy {
	return &TextProxy{
		Text: datatype.NewText(createdAt),
		context: ctx,
	}
}