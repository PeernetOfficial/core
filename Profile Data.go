/*
File Name:  Profile Data.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

// List of recognized profile fields.
const (
	ProfileName    = 0 // Arbitrary username
	ProfileEmail   = 1 // Email address
	ProfileWebsite = 2 // Website address
	ProfileTwitter = 3 // Twitter account without the @
	ProfileYouTube = 4 // YouTube channel URL
	ProfileAddress = 5 // Physical address
	ProfilePicture = 6 // Profile picture, blob
)

// The encoding of profile fields depends on the field. Text data is always UTF-8 text encoded.
// Note that all profile data is arbitrary and shall be considered untrusted and unverified.
// To establish trust, the user must load Certificates into the blockchain that validate certain data.

// Text returns the profile field as text encoded
func (info *BlockRecordProfile) Text() string {
	return string(info.Data)
}

// ProfileFieldFromText returns a profile field from text
func ProfileFieldFromText(Type uint16, Text string) BlockRecordProfile {
	return BlockRecordProfile{Type: Type, Data: []byte(Text)}
}
