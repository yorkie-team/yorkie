package proxy

import (
	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type TextProxy struct {
	*json.Text
	context *change.Context
}

func ProxyText(ctx *change.Context, text *json.Text) *TextProxy {
	return &TextProxy{
		Text:    text,
		context: ctx,
	}
}

func NewTextProxy(
	ctx *change.Context,
	rgaTreeSplit *json.RGATreeSplit,
	createdAt *time.Ticket,
) *TextProxy {
	return &TextProxy{
		Text:    json.NewText(rgaTreeSplit, createdAt),
		context: ctx,
	}
}

func (p *TextProxy) Edit(from, to int, content string) *TextProxy {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.Text.CreateRange(from, to)
	log.Logger.Debugf(
		"EDIT: f:%d->%s, t:%d->%s c:%s",
		from, fromPos.AnnotatedString(), to, toPos.AnnotatedString(), content,
	)

	ticket := p.context.IssueTimeTicket()
	_, maxCreationMapByActor := p.Text.Edit(
		fromPos,
		toPos,
		nil,
		content,
		ticket,
	)

	p.context.Push(operation.NewEdit(
		p.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		content,
		ticket,
	))

	return p
}
