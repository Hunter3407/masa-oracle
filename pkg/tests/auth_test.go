package tests

import (
	"os"
	"testing"

	"github.com/masa-finance/masa-oracle/pkg/twitter"
	twitterscraper "github.com/n0madic/twitter-scraper"
	"github.com/sirupsen/logrus"
)

func TestAuth(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)
	scraper := twitterscraper.New()
	username := ""
	password := ""
	logrus.WithFields(logrus.Fields{"username": username}).Debug("Attempting to login")

	// Attempt to retrieve a 2FA code from environment variables
	twoFACode := os.Getenv("TWITTER_2FA_CODE")
	if twoFACode != "" {
		logrus.WithField("2FA", "provided").Debug("2FA code is provided, attempting login with 2FA")
	} else {
		logrus.Debug("No 2FA code provided, attempting basic login")
	}

	var err error
	if twoFACode != "" {
		// If a 2FA code is provided, use it for login
		err = twitter.Login(scraper, username, password, twoFACode)
	} else {
		// Otherwise, proceed with basic login
		err = twitter.Login(scraper, username, password)
	}

	if err != nil {
		logrus.WithError(err).Fatal("Login failed")
	} else {
		logrus.Debug("Login successful")
	}

	// Optionally, check if logged in
	if twitter.IsLoggedIn(scraper) {
		logrus.Debug("Confirmed logged in.")
	} else {
		logrus.Debug("Not logged in.")
	}

	// Don't forget to logout after testing
	err = twitter.Logout(scraper)
	if err != nil {
		logrus.WithError(err).Warn("Logout failed")
	} else {
		logrus.Debug("Logged out successfully.")
	}
}
