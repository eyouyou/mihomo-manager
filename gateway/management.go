package main

import (
	"net/http"
	"path"
	"strings"
)

const managementPath = "/management"
const managementRulesPage = managementPath + "/?view=rules"

func serveManagement(w http.ResponseWriter, r *http.Request, directory string, assets http.Handler) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	p := r.URL.Path
	switch {
	case p == managementPath+"/":
		http.ServeFile(w, r, directory+"/index.html")
	case strings.HasPrefix(p, managementPath+"/assets/") && path.Clean(p) == p && path.Ext(p) != "":
		assets.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}
