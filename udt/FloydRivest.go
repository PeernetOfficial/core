package udt

import (
	"math"
	"sort"
)

// fork from github.com/furstenheim/nth_element/FloydRivest

// FloydRivestBuckets. Sort a slice into buckets of given size. All elements from one bucket are smaller than any element  from the next one.
// elements at position i * bucketSize are guaranteed to be the (i * bucketSize) th smallest elements
// s := // some slice
// FloydRivest.Buckets(sort.Interface(s), 5)
// s is now sorted into buckets of size 5
// max(s[0:5]) < min(s[5:10])
// max(s[10: 15]) < min(s[15:20])
// ...
func FloydRivestBuckets(slice sort.Interface, bucketSize int) {
	left := 0
	right := slice.Len() - 1
	s := floydRivestStack([]int{left, right})
	var mid int
	for len(s) > 0 {
		s, right = s.Pop()
		s, left = s.Pop()
		if right-left <= bucketSize {
			continue
		}
		// + bucketSize - 1 is to do math ceil
		mid = left + ((right-left+bucketSize-1)/bucketSize/2)*bucketSize
		FloydRivestSelect(slice, mid, left, right)
		s = s.Push(left)
		s = s.Push(mid)
		s = s.Push(mid)
		s = s.Push(right)
	}
}

// left is the left index for the interval
// right is the right index for the interval
// k is the desired index value, where array[k] is the k+1 smallest element
// when left = 0
func FloydRivestSelect(array sort.Interface, k, left, right int) {
	length := array.Len()
	for right > left {
		if right-left > 600 {
			var n = float64(right - left + 1)
			var kf = float64(k)
			var m = float64(k - left + 1)
			var z = math.Log(n)
			var s = 0.5 * math.Exp(2*z/3)
			sign := float64(1)
			if m-n/2 < 0 {
				sign = -1
			}
			var sd = 0.5 * math.Sqrt(z*s*(n-s)/n) * sign
			var newLeft = intMax(left, int(math.Floor(kf-m*s/n+sd)))
			var newRight = intMin(right, int(math.Floor(kf+(n-m)*s/n+sd)))
			FloydRivestSelect(array, k, newLeft, newRight)
		}

		var i = left
		var j = right
		array.Swap(left, k)
		// in the original algorithm array[k] is stored to a value. To use golangs sort interface we need to keep track of the changes for the index
		// we define it as right because in the first iteration of for i<j it will be changed
		pointIndex := right
		if array.Less(left, right) {
			array.Swap(left, right)
			pointIndex = left
		}

		for i < j {
			// pointIndex is swapped only once in the first iteration. Later it will either be bigger (if left) or smaller (if right)
			array.Swap(i, j)
			i++
			j--
			for i < length && array.Less(i, pointIndex) {
				i++
			}
			for j >= 0 && array.Less(pointIndex, j) {
				j--
			}
		}
		// All equal points
		if !array.Less(left, pointIndex) && !array.Less(pointIndex, left) {
			array.Swap(left, j)
		} else {
			j++
			array.Swap(j, right)
		}
		if j <= k {
			left = j + 1
		}
		if k <= j {
			right = j - 1
		}
	}
}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type floydRivestStack []int

func (s floydRivestStack) Push(v int) floydRivestStack {
	return append(s, v)
}
func (s floydRivestStack) Pop() (floydRivestStack, int) {
	l := len(s)
	return s[:l-1], s[l-1]
}
