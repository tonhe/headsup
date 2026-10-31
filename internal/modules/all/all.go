// Package all enables every built-in module. Adding a module means one
// directory plus one import line here.
package all

import (
	_ "github.com/tonhe/headsup/internal/modules/dashboard"
	_ "github.com/tonhe/headsup/internal/modules/keys"
	_ "github.com/tonhe/headsup/internal/modules/machines"
	_ "github.com/tonhe/headsup/internal/modules/pending"
	_ "github.com/tonhe/headsup/internal/modules/prefs"
	_ "github.com/tonhe/headsup/internal/modules/routes"
	_ "github.com/tonhe/headsup/internal/modules/serverconfig"
	_ "github.com/tonhe/headsup/internal/modules/service"
	_ "github.com/tonhe/headsup/internal/modules/users"
)
