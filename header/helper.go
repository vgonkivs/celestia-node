package header

import (
	"bytes"
	"sort"
)

// GetFrequentHeader chooses the most frequent header.
// NOTE: if slice does not have duplicates, then GetFrequentHeader returns the first entry of the passed slice.
func GetFrequentHeader(headers []*ExtendedHeader) *ExtendedHeader {
	// sort in the decreasing order
	sort.Slice(headers, func(i, j int) bool {
		return bytes.Compare(headers[i].Hash(), headers[j].Hash()) < 0
	})
	maxCount, currCount := 1, 1
	res := headers[0]
	for i := 1; i < len(headers); i++ {
		// compare current entry with the previous one and increase counter if
		// they have equal hashes, otherwise reset counter
		if headers[i].Hash().String() == headers[i-1].Hash().String() {
			currCount++
		} else {
			currCount = 1
		}

		// check if current counter is more maxCount
		if currCount > maxCount {
			// update maxCount
			maxCount = currCount
			// set more frequent head
			res = headers[i]
		}
	}

	return res
}
