package cgouse

/*
#include "../native_shared.h"
*/
import "C"
import _ "example.com/libnix-fixture/cgobase"

func Answer() int { return int(C.libnix_answer()) }
