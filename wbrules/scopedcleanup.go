package wbrules

const (
	SCOPE_STACK_CAPACITY  = 10
	CLEANUP_LIST_CAPACITY = 10
)

type CleanupFunc func()
type cleanupMap map[string][]CleanupFunc

// ScopedCleanup manages a list of cleanup functions
// that must be invoked when some named scope ceases
// to exist.

type ScopedCleanup struct {
	cleanupLists cleanupMap
	scopeStack   []string
}

func MakeScopedCleanup() *ScopedCleanup {
	return &ScopedCleanup{
		make(cleanupMap),
		make([]string, 0, SCOPE_STACK_CAPACITY),
	}
}

func (sc *ScopedCleanup) PushCleanupScope(scope string) string {
	if scope == "" {
		panic("trying to push an empty scope")
	}
	sc.scopeStack = append(sc.scopeStack, scope)
	return scope
}

func (sc *ScopedCleanup) PopCleanupScope(scope string) string {
	top := len(sc.scopeStack) - 1
	if top < 0 || sc.scopeStack[top] != scope {
		panic("scoped cleanup stack error")
	}
	sc.scopeStack = sc.scopeStack[:top]
	return scope
}

func (sc *ScopedCleanup) AddCleanup(cleanupFn CleanupFunc) {
	if len(sc.scopeStack) == 0 {
		// global scope, cleanup will not run
		return
	}
	scope := sc.scopeStack[len(sc.scopeStack)-1]
	l, found := sc.cleanupLists[scope]
	if !found {
		l = make([]CleanupFunc, 0, CLEANUP_LIST_CAPACITY)
	}
	sc.cleanupLists[scope] = append(l, cleanupFn)
}

func (sc *ScopedCleanup) RunCleanups(scope string) {
	l, found := sc.cleanupLists[scope]
	if !found {
		return
	}
	defer delete(sc.cleanupLists, scope)
	for _, cleanupFn := range l {
		cleanupFn()
	}
}
