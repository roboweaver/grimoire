package web

import "github.com/roboweaver/grimoire/internal/content"

func (s *Server) WithContentFeatures(comments *content.CommentService, media *content.MediaService, menus *content.NavMenuService) *Server {
	s.comments = comments
	s.media = media
	s.menus = menus
	return s
}

// WithMediaWrites enables the REST media write surface (POST
// /wp-json/wp/v2/media, Req 1.1-1.6) by wiring the capability-checked
// *content.MediaWriteService as s.mediaWrite. It returns the same Server for
// chaining. Mirroring WithAdminWrites, this is a non-breaking builder rather
// than a new positional parameter on WithContentFeatures (which would break
// its existing call sites). When mw is nil, registerRESTMedia keeps the 501
// restNotImplemented stub for POST /media.
func (s *Server) WithMediaWrites(mw *content.MediaWriteService) *Server {
	s.mediaWrite = mw
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
