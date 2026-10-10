package report

type User struct{ Name string }

// Name returns the user's display name.
func Name(u *User) string {
	return u.Name
}
