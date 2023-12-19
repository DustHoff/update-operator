package helper

import (
	"github.com/DustHoff/update-operator/api/v1alpha1"
)

func QuickSort(arr []v1alpha1.NodeUpdate) []v1alpha1.NodeUpdate {
	// return the array if array length is 1 or less than 1
	if len(arr) <= 1 {
		return arr
	}

	// make the first element as pivot
	pivot := arr[0]

	i := len(arr) - 1

	j := 1

	// iterate till i and j are not passed each other
	for i < j {
		for i < len(arr) {
			if arr[i].Spec.Priority < pivot.Spec.Priority {
				i++
			} else {
				break
			}
		}

		for j > 0 {
			if arr[j].Spec.Priority > pivot.Spec.Priority {
				j--
			} else {
				break
			}
		}

		// if i is less than j
		// swap the value at i and j index
		// swap the larger value at i with lesser value at j
		if i < j {
			arr[i], arr[j] = arr[j], arr[i]
		}
	}

	// Swap the value at j with pivot
	// j index is the sorted position of the pivot
	// everything to its left is lesser and larger to its right.
	arr[j], arr[0] = arr[0], arr[j]

	// left side of the pivot will be a new subarray
	left := arr[:j]

	// if the length of the subarray is greater than 1
	// then QuickSort the subarray
	if len(left) > 1 {
		left = QuickSort(left)
	}

	// right side of the pivot will be a new subarray
	right := arr[j+1:]

	// if the length of the subarray is greater than 1
	// then QuickSort the subarray
	if len(right) > 1 {
		right = QuickSort(right)
	}

	// concat all the elements of the array
	// left pivot and right
	// return the sorted array
	left = append(left, pivot)

	return append(left, right...)
}
