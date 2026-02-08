package util

func IsGuestID(id string) bool {
	return len(id) > 2 && id[:2] == "g_"
}
