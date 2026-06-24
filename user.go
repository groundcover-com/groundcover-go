package groundcover

// User identifies the principal associated with an event. Organization is the
// B2B group key used for attribution; it has no OTel convention and is a
// groundcover extension.
type User struct {
	// ID is a stable user identifier.
	ID string
	// Email is the user's email address.
	Email string
	// Name is a human-readable user name.
	Name string
	// Organization is the B2B group/tenant key.
	Organization string
}

// isZero reports whether the user carries no information.
func (u User) isZero() bool {
	return u.ID == "" && u.Email == "" && u.Name == "" && u.Organization == ""
}
