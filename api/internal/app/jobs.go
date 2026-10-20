package app

import "net/http"

func (a *Server) jobs(w http.ResponseWriter, r *http.Request, p Principal) error {
	b, e := a.Store.JSON(r.Context(), `SELECT COALESCE(jsonb_agg(x),'[]') FROM(SELECT id,kind,state,attempts,error,created_at,run_at FROM jobs WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 100)x`, p.Workspace)
	if e != nil {
		return e
	}
	write(w, 200, b)
	return nil
}
