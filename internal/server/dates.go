package server

import (
	"strconv"
	"strings"
)

var monthNames = map[string][12]string{
	"nb":    {"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"},
	"en-US": {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
}

// fmtDate formats YYYY, YYYY-MM or YYYY-MM-DD for people: "12. mars 2019"
// in Norwegian, "March 12, 2019" in English. Anything else is returned as is.
func fmtDate(lang, s string) string {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if s == "" || len(parts) > 3 || len(parts[0]) != 4 {
		return s
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return s
		}
		nums[i] = n
	}
	if len(nums) == 1 {
		return parts[0]
	}
	if nums[1] < 1 || nums[1] > 12 {
		return s
	}
	names, ok := monthNames[lang]
	if !ok {
		names = monthNames["nb"]
	}
	month := names[nums[1]-1]
	if len(nums) == 2 {
		return month + " " + parts[0]
	}
	if nums[2] < 1 || nums[2] > 31 {
		return s
	}
	if lang == "nb" {
		return strconv.Itoa(nums[2]) + ". " + month + " " + parts[0]
	}
	return month + " " + strconv.Itoa(nums[2]) + ", " + parts[0]
}
