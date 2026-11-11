package motion

// ErrEnded is the error a refused move returns.
var ErrEnded = errEnded

// ClosePending reports whether a Close request is parked, waiting for the hold's goroutine.
func ClosePending(svc Service, holdID string) bool {
	s, err := svc.(*service).find(holdID)
	return err == nil && len(s.closeReq) > 0
}
