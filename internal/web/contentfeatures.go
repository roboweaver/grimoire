package web

import "github.com/roboweaver/grimoire/internal/content"

func (s *Server) WithContentFeatures(comments *content.CommentService, media *content.MediaService, menus *content.NavMenuService) *Server {
	s.comments = comments
	s.media = media
	s.menus = menus
	return s
}

// WithFeaturedImages wires the featured-image lookup used to populate
// PostView.FeaturedImageURL on the home, category, and single/page views.
// featured may be nil, in which case featured images are simply omitted from
// every card/post (FeaturedImageService.URL is nil-receiver safe).
func (s *Server) WithFeaturedImages(featured *content.FeaturedImageService) *Server {
	s.featured = featured
	return s
}
