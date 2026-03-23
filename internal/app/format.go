package app

import "strconv"

func intString(value int) string {
	return strconv.Itoa(value)
}

func formatPercent(value int) string {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return strconv.Itoa(value) + "%"
}

func formatProgress(done, total int) string {
	if done < 0 {
		done = 0
	}
	if total < 0 {
		total = 0
	}
	if done > total && total > 0 {
		done = total
	}
	return strconv.Itoa(done) + " / " + strconv.Itoa(total)
}
