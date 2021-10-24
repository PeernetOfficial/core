package udt

import "time"

type sortableDurnArray []time.Duration

func (a sortableDurnArray) Len() int {
	return len(a)
}

func (a sortableDurnArray) Less(i, j int) bool {
	return a[i] < a[j]
}

func (a sortableDurnArray) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}
