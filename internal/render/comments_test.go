package render

import (
	"html/template"
	"reflect"
	"testing"
)

// TestCommentViewContentIsTemplateHTML asserts the write-boundary content-safety
// contract at the render layer (Req 6.1, 6.5): CommentView.Content carries
// pre-sanitized comment markup and must therefore be template.HTML so it passes
// through the template engine without a second auto-escape, while
// CommentView.Author remains a plain string that the engine auto-escapes.
func TestCommentViewContentIsTemplateHTML(t *testing.T) {
	ct := reflect.TypeOf(CommentView{})

	content, ok := ct.FieldByName("Content")
	if !ok {
		t.Fatal("CommentView has no field Content")
	}
	if content.Type != reflect.TypeOf(template.HTML("")) {
		t.Errorf("CommentView.Content type = %v, want template.HTML", content.Type)
	}

	author, ok := ct.FieldByName("Author")
	if !ok {
		t.Fatal("CommentView has no field Author")
	}
	if author.Type != reflect.TypeOf(string("")) {
		t.Errorf("CommentView.Author type = %v, want string", author.Type)
	}
}
