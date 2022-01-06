package middleware

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/RedHatInsights/rbac-client-go"
	"github.com/RedHatInsights/sources-api-go/config"
	"github.com/RedHatInsights/sources-api-go/util"
	"github.com/labstack/echo/v4"
	"github.com/redhatinsights/platform-go-middlewares/identity"
)

var PSKS = config.Get().Psks

/*
	Takes the information stored in the context and returns a 401 if we do not
	have authorization to perform "write" things such as POST/PATCH/DELETE.

	1. Checks for PSK (if present) and if it is there and matches any of the
	   PSKs we approve, lets it through.

	2. Sends the x-rh-identity header off to rbac to get an ACL list, and
	   returns whether or not it contains the correct `sources:*:*` permission.
*/
func PermissionCheck(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		switch {
		case config.Get().BypassRbac:
			c.Logger().Debugf("Skipping authorization check -- disabled in ENV")
		case c.Get("psk") != nil:
			psk, ok := c.Get("psk").(string)
			if !ok {
				return fmt.Errorf("error casting psk to string: %v", c.Get("psk"))
			}

			if !pskMatches(psk) {
				return c.JSON(http.StatusUnauthorized, util.ErrorDoc("Unauthorized Action: Incorrect PSK", "401"))
			}

		case c.Get("x-rh-identity") != nil:
			// first check the identity (already parsed) to see if it contains
			// the system key and if it does do some extra checks to authorize
			// based on some internal rules (operator + satellite)
			identity, ok := c.Get("identity").(identity.XRHID)
			if !ok {
				return fmt.Errorf("error casting identity to struct: %+v", c.Get("identity"))
			}

			// current sources-api behavior = if there is a system key -> it's authorized.
			// TODO: make this more specific and do more checks.
			if identity.Identity.System != nil {
				break
			}

			// otherwise, ship the xrhid off to rbac and check access rights.
			rhid, ok := c.Get("x-rh-identity").(string)
			if !ok {
				return fmt.Errorf("error casting x-rh-identity to string: %v", c.Get("x-rh-identity"))
			}

			allowed, err := rbacAllowed(rhid)
			if err != nil {
				return fmt.Errorf("error hitting rbac: %v", err)
			}

			if !allowed {
				return c.JSON(http.StatusUnauthorized, util.ErrorDoc("Unauthorized Action: Missing RBAC permissions", "401"))
			}

		default:
			return c.JSON(http.StatusUnauthorized, util.ErrorDoc("Authentication required by either [x-rh-identity] or [x-rh-sources-psk]", "401"))
		}

		return next(c)
	}
}

func pskMatches(psk string) bool {
	return util.SliceContainsString(PSKS, psk)
}

var r = rbac.NewClient(os.Getenv("RBAC_URL"), "sources")

// fetches an access list from RBAC based on RBAC_URL and returns whether or not
// the xrhid has the `sources:*:*` permission
func rbacAllowed(xrhid string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	acl, err := r.GetAccess(ctx, xrhid, "")
	if err != nil {
		return false, err
	}

	return acl.IsAllowed("sources", "*", "*"), nil
}