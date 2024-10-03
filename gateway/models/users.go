package models

import (
	"github.com/hoophq/hoop/common/log"
)

type UserGroup struct {
	OrgID            string
	UserID           string
	ServiceAccountId string
	Name             string
}

type User struct {
	ID       string
	OrgID    string
	Subject  string
	Name     string
	Picture  string
	Email    string
	Verified bool
	Status   string
	SlackID  string
	Groups   []string `gorm:"-"`
	// Groups   []string `json:"groups"`
	//Org      *Org     `json:"orgs"`
	// used for local auth only
	HashedPassword string
}

func ListUsers(orgID string) ([]User, error) {
	log.Infof("listing users for org=%s", orgID)
	var users []User
	if err := DB.Where("org_id = ?", orgID).Find(&users).Error; err != nil {
		log.Errorf("failed to list users, reason=%v", err)
		return nil, err
	}

	for i := range users {
		var userGroups []UserGroup
		if err := DB.Where("org_id = ? AND user_id = ?", orgID, users[i].ID).Find(&userGroups).Error; err != nil {
			log.Errorf("failed to list user groups, reason=%v", err)
			return nil, err
		}

		for j := range userGroups {
			users[i].Groups = append(users[i].Groups, userGroups[j].Name)
		}
	}

	return users, nil
}

func GetUser(orgID, userID string) (User, error) {
	log.Infof("getting user=%s for org=%s", userID, orgID)
	var user User
	if err := DB.Where("org_id = ? AND id = ?", orgID, userID).First(&user).Error; err != nil {
		log.Errorf("failed to get user, reason=%v", err)
		return User{}, err
	}

	var userGroups []UserGroup
	if err := DB.Where("org_id = ? AND user_id = ?", orgID, user.ID).Find(&userGroups).Error; err != nil {
		log.Errorf("failed to list user groups, reason=%v", err)
		return User{}, err
	}

	for j := range userGroups {
		user.Groups = append(user.Groups, userGroups[j].Name)
	}

	return user, nil
}
