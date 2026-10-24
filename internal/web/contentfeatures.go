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

// WithUserWrites enables the REST user write surface (Req 1, 7) by wiring the
// capability-checked *content.UserService as s.userWrite. It returns the same
// Server for chaining. Mirroring WithMediaWrites, this is a non-breaking builder
// rather than a new positional parameter. When uw is nil, registerRESTUsers
// keeps the 501 restNotImplemented stubs for the user write verbs.
func (s *Server) WithUserWrites(uw *content.UserService) *Server {
	s.userWrite = uw
	return s
}

// WithNavMenuWrites enables the admin-API menu write surface (POST/PUT/DELETE on
// /admin/api/menus and /menus/{id}/items, M10.F Req 1, 2). When nmw is nil the
// write group is not registered and the server keeps serving the existing GET
// read routes unchanged (Req 3.3). It returns the same Server for chaining,
// mirroring WithMediaWrites/WithUserWrites.
func (s *Server) WithNavMenuWrites(nmw *content.NavMenuWriteService) *Server {
	s.navMenuWrite = nmw
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
