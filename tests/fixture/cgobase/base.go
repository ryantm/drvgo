package cgobase

/*
#include "../native_shared.h"
int libnix_answer(void) { return 42; }
*/
import "C"
