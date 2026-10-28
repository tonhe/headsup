// Package all enables every built-in module. Adding a module means one
// directory plus one import line here.
package all

import (
	_ "github.com/tonhe/fathom/internal/modules/dashboard"
	_ "github.com/tonhe/fathom/internal/modules/machines"
	_ "github.com/tonhe/fathom/internal/modules/pending"
)
