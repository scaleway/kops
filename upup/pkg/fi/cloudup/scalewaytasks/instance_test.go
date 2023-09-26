package scalewaytasks

import (
	"strconv"
	"testing"

	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
)

func TestFindFirstFreeIndex(t *testing.T) {
	igName := "control-plane-1"
	type TestCase struct {
		Actual   []int
		Expected int
	}
	testCases := []TestCase{
		{
			[]int{0, 2, 1},
			3,
		},
		{
			[]int{1, 2, 4},
			0,
		},
		{
			[]int{4, 5, 2, 3, 0},
			1,
		},
		{
			[]int{0, 1, 2, 3},
			4,
		},
	}

	for _, testCase := range testCases {
		existing := []*instance.Server(nil)
		for _, i := range testCase.Actual {
			existing = append(existing, &instance.Server{Name: igName + "-" + strconv.Itoa(i)})
		}
		index := findFirstFreeIndex(existing)
		if index != testCase.Expected {
			t.Errorf("Expected %d, got %d", testCase.Expected, index)
		}
	}
}
