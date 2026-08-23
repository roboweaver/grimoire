package web

import "github.com/roboweaver/grimoire/internal/content"

func (s *Server) WithContentFeatures(comments *content.CommentService, media *content.MediaService, menus *content.NavMenuService) *Server {
	s.comments = comments
	s.media = media
	s.menus = menus
	return s
}
