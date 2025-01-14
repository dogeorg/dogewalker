package util

// Unordered remove element from array.
func Remove[S ~[]E, E comparable](array S, elem E) S {
	for i := 0; i < len(array); i++ {
		if array[i] == elem {
			array[i] = array[len(array)-1]
			return array[:len(array)-1]
		}
	}
	return array
}
