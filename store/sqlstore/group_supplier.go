// Copyright (c) 2018-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package sqlstore

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/mattermost/gorp"
	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/store"
)

type groupMembers []model.GroupMember

func initSqlSupplierGroups(sqlStore SqlStore) {
	for _, db := range sqlStore.GetAllConns() {
		groups := db.AddTableWithName(model.Group{}, "Groups").SetKeys(false, "Id")
		groups.ColMap("Id").SetMaxSize(26)
		groups.ColMap("Name").SetMaxSize(model.GroupNameMaxLength).SetUnique(true)
		groups.ColMap("DisplayName").SetMaxSize(model.GroupDisplayNameMaxLength)
		groups.ColMap("Description").SetMaxSize(model.GroupDescriptionMaxLength)
		groups.ColMap("Type").SetMaxSize(model.GroupTypeMaxLength)
		groups.ColMap("TypeProps").SetMaxSize(model.GroupTypePropsMaxLength)

		groupMembers := db.AddTableWithName(model.GroupMember{}, "GroupMembers").SetKeys(false, "GroupId", "UserId")
		groupMembers.ColMap("GroupId").SetMaxSize(26)
		groupMembers.ColMap("UserId").SetMaxSize(26)

		groupTeams := db.AddTableWithName(model.GroupTeam{}, "GroupTeams").SetKeys(false, "GroupId", "TeamId")
		groupTeams.ColMap("GroupId").SetMaxSize(26)
		groupTeams.ColMap("TeamId").SetMaxSize(26)

		groupChannels := db.AddTableWithName(model.GroupChannel{}, "GroupChannels").SetKeys(false, "GroupId", "ChannelId")
		groupChannels.ColMap("GroupId").SetMaxSize(26)
		groupChannels.ColMap("ChannelId").SetMaxSize(26)
	}
}

func (s *SqlSupplier) GroupSave(ctx context.Context, group *model.Group, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()
	var err error

	if len(group.Id) == 0 {
		if err := group.IsValidForCreate(); err != nil {
			result.Err = err
			return result
		}

		var transaction *gorp.Transaction

		if transaction, err = s.GetMaster().Begin(); err != nil {
			result.Err = model.NewAppError("SqlGroupStore.GroupSave", "store.sql_group.save.open_transaction.app_error", nil, err.Error(), http.StatusInternalServerError)
			return result
		}

		result = s.createGroup(ctx, group, transaction, hints...)

		if result.Err != nil {
			transaction.Rollback()
		} else if err := transaction.Commit(); err != nil {
			result.Err = model.NewAppError("SqlGroupStore.GroupSave", "store.sql_group.save_group.commit_transaction.app_error", nil, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := group.IsValidForUpdate(); err != nil {
			result.Err = err
			return result
		}

		group.UpdateAt = model.GetMillis()

		var retrievedGroup *model.Group
		if err := s.GetMaster().SelectOne(&retrievedGroup, "SELECT * FROM Groups WHERE Id = :Id", map[string]interface{}{"Id": group.Id}); err != nil {
			if err == sql.ErrNoRows {
				result.Err = model.NewAppError("SqlGroupStore.Save", "store.sql_group.save.missing.app_error", nil, "id="+group.Id+","+err.Error(), http.StatusNotFound)
				return result
			}
			result.Err = model.NewAppError("SqlGroupStore.SaveMember", "store.sql_group.save.app_error", nil, "id="+group.Id+","+err.Error(), http.StatusInternalServerError)
			return result
		}

		if rowsChanged, err := s.GetMaster().Update(group); err != nil {
			result.Err = model.NewAppError("SqlGroupStore.Save", "store.sql_group.save.update.app_error", nil, err.Error(), http.StatusInternalServerError)
		} else if rowsChanged != 1 {
			result.Err = model.NewAppError("SqlGroupStore.Save", "store.sql_group.save.update.app_error", nil, "no record to update", http.StatusInternalServerError)
		}

		result.Data = group
	}

	return result
}

func (s *SqlSupplier) createGroup(ctx context.Context, group *model.Group, transaction *gorp.Transaction, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	if err := group.IsValidForCreate(); err != nil {
		result.Err = err
		return result
	}

	group.Id = model.NewId()
	group.CreateAt = model.GetMillis()
	group.UpdateAt = group.CreateAt

	if err := transaction.Insert(group); err != nil {
		result.Err = model.NewAppError("SqlGroupStore.Save", "store.sql_group.save.insert.app_error", nil, err.Error(), http.StatusInternalServerError)
		return result
	}

	result.Data = group

	return result
}

func (s *SqlSupplier) GroupGet(ctx context.Context, groupId string, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	var group *model.Group

	if err := s.GetReplica().SelectOne(&group, "SELECT * from Groups WHERE Id = :Id", map[string]interface{}{"Id": groupId}); err != nil {
		if err == sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.Get", "store.sql_group.get.app_error", nil, "Id="+groupId+", "+err.Error(), http.StatusNotFound)
		} else {
			result.Err = model.NewAppError("SqlGroupStore.Get", "store.sql_group.get.app_error", nil, err.Error(), http.StatusInternalServerError)
		}
	}

	result.Data = group

	return result
}

func (s *SqlSupplier) GroupGetAllPage(ctx context.Context, offset int, limit int, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	var groups []*model.Group

	if _, err := s.GetReplica().Select(&groups, "SELECT * from Groups WHERE DeleteAt = 0 ORDER BY CreateAt DESC LIMIT :Limit OFFSET :Offset", map[string]interface{}{"Limit": limit, "Offset": offset}); err != nil {
		result.Err = model.NewAppError("SqlGroupStore.Get", "store.sql_group.get.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	result.Data = groups

	return result
}

func (s *SqlSupplier) GroupDelete(ctx context.Context, groupId string, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	if !model.IsValidId(groupId) {
		result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete.group_id.invalid", nil, "Id="+groupId, http.StatusBadRequest)
	}

	var group *model.Group
	if err := s.GetReplica().SelectOne(&group, "SELECT * from Groups WHERE Id = :Id", map[string]interface{}{"Id": groupId}); err != nil {
		if err == sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.get.app_error", nil, "Id="+groupId+", "+err.Error(), http.StatusNotFound)
		} else {
			result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.get.app_error", nil, err.Error(), http.StatusInternalServerError)
		}

		return result
	}

	time := model.GetMillis()
	group.DeleteAt = time
	group.UpdateAt = time

	if rowsChanged, err := s.GetMaster().Update(group); err != nil {
		result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete.update.app_error", nil, err.Error(), http.StatusInternalServerError)
	} else if rowsChanged != 1 {
		result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete.update.app_error", nil, "no record to update", http.StatusInternalServerError)
	} else {
		result.Data = group
	}

	return result
}

func (s *SqlSupplier) GroupCreateMember(ctx context.Context, groupID string, userID string, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	member := &model.GroupMember{
		GroupId:  groupID,
		UserId:   userID,
		CreateAt: model.GetMillis(),
	}

	if result.Err = member.IsValid(); result.Err != nil {
		return result
	}

	if err := s.GetMaster().Insert(member); err != nil {
		if IsUniqueConstraintError(err, []string{"GroupId", "UserId", "groupmembers_pkey", "PRIMARY"}) {
			result.Err = model.NewAppError("SqlGroupStore.CreateMember", "store.sql_group.save_member.exists.app_error", nil, "group_id="+member.GroupId+", user_id="+member.UserId+", "+err.Error(), http.StatusBadRequest)
			return result
		}
		result.Err = model.NewAppError("SqlGroupStore.CreateMember", "store.sql_group.save_member.save.app_error", nil, "group_id="+member.GroupId+", user_id="+member.UserId+", "+err.Error(), http.StatusInternalServerError)
		return result
	}

	var retrievedMember *model.GroupMember
	if err := s.GetMaster().SelectOne(&retrievedMember, "SELECT * FROM GroupMembers WHERE GroupId = :GroupId AND UserId = :UserId", map[string]interface{}{"GroupId": member.GroupId, "UserId": member.UserId}); err != nil {
		if err == sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.CreateMember", "store.sql_group.get_member.missing.app_error", nil, "group_id="+member.GroupId+"user_id="+member.UserId+","+err.Error(), http.StatusNotFound)
			return result
		}
		result.Err = model.NewAppError("SqlGroupStore.CreateMember", "store.sql_group.get_member.app_error", nil, "group_id="+member.GroupId+"user_id="+member.UserId+","+err.Error(), http.StatusInternalServerError)
		return result
	}
	result.Data = retrievedMember
	return result
}

func (s *SqlSupplier) GroupDeleteMember(ctx context.Context, groupID string, userID string, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	if !model.IsValidId(groupID) {
		result.Err = model.NewAppError("SqlGroupStore.DeleteMember", "model.group_member.group_id.app_error", nil, "", http.StatusBadRequest)
		return result
	}
	if !model.IsValidId(userID) {
		result.Err = model.NewAppError("SqlGroupStore.DeleteMember", "model.group_member.user_id.app_error", nil, "", http.StatusBadRequest)
		return result
	}

	var retrievedMember *model.GroupMember
	if err := s.GetMaster().SelectOne(&retrievedMember, "SELECT * FROM GroupMembers WHERE GroupId = :GroupId AND UserId = :UserId", map[string]interface{}{"GroupId": groupID, "UserId": userID}); err != nil {
		if err == sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.DeleteMember", "store.sql_group.get_member.missing.app_error", nil, "group_id="+groupID+"user_id="+userID+","+err.Error(), http.StatusNotFound)
			return result
		}
		result.Err = model.NewAppError("SqlGroupStore.DeleteMember", "store.sql_group.get_member.app_error", nil, "group_id="+groupID+"user_id="+userID+","+err.Error(), http.StatusInternalServerError)
		return result
	}

	if retrievedMember.DeleteAt != 0 {
		result.Err = model.NewAppError("SqlGroupStore.DeleteMember", "store.sql_group.delete_member.already_deleted", nil, "group_id="+groupID+"user_id="+userID, http.StatusInternalServerError)
		return result
	}

	retrievedMember.DeleteAt = model.GetMillis()

	if rowsChanged, err := s.GetMaster().Update(retrievedMember); err != nil {
		result.Err = model.NewAppError("SqlGroupStore.delete_member", "store.sql_scheme.delete_member.update.app_error", nil, err.Error(), http.StatusInternalServerError)
	} else if rowsChanged != 1 {
		result.Err = model.NewAppError("SqlGroupStore.delete_member", "store.sql_scheme.delete_member.update.app_error", nil, "no record to update", http.StatusInternalServerError)
	}

	result.Data = retrievedMember

	return result
}

func (s *SqlSupplier) GroupSaveGroupTeam(ctx context.Context, groupTeam *model.GroupTeam, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	if err := groupTeam.IsValid(); err != nil {
		result.Err = err
		return result
	}

	var retrievedGroupTeam *model.GroupTeam
	if err := s.GetMaster().SelectOne(&retrievedGroupTeam, "SELECT * FROM GroupTeams WHERE GroupId = :GroupId AND TeamId = :TeamId", map[string]interface{}{"GroupId": groupTeam.GroupId, "TeamId": groupTeam.TeamId}); err != nil {
		if err != sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.CreateMember", "store.sql_group.save_group_team.app_error", nil, "group_id="+groupTeam.GroupId+"team_id="+groupTeam.TeamId+","+err.Error(), http.StatusInternalServerError)
			return result
		}
	}

	groupTeam.UpdateAt = model.GetMillis()

	if retrievedGroupTeam == nil {
		groupTeam.CreateAt = groupTeam.UpdateAt
		if err := s.GetMaster().Insert(groupTeam); err != nil {
			if IsUniqueConstraintError(err, []string{"GroupId", "TeamId", "groupteams_pkey", "PRIMARY"}) {
				result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save_group_team.exists.app_error", nil, "group_id="+groupTeam.GroupId+", team_id="+groupTeam.TeamId+", "+err.Error(), http.StatusBadRequest)
				return result
			}
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save_group_team.save.app_error", nil, "group_id="+groupTeam.GroupId+", team_id="+groupTeam.TeamId+", "+err.Error(), http.StatusInternalServerError)
			return result
		}
	} else {
		if rowsChanged, err := s.GetMaster().Update(groupTeam); err != nil {
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save.update.app_error", nil, err.Error(), http.StatusInternalServerError)
			return result
		} else if rowsChanged != 1 {
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save.update.app_error", nil, "no record to update", http.StatusInternalServerError)
			return result
		}
	}

	if err := s.GetMaster().SelectOne(&retrievedGroupTeam, "SELECT * FROM GroupTeams WHERE GroupId = :GroupId AND TeamId = :TeamId", map[string]interface{}{"GroupId": groupTeam.GroupId, "TeamId": groupTeam.TeamId}); err != nil {
		if err != sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save_group_team.app_error", nil, "group_id="+groupTeam.GroupId+"team_id="+groupTeam.TeamId+","+err.Error(), http.StatusInternalServerError)
			return result
		}
	}

	result.Data = retrievedGroupTeam
	return result
}

func (s *SqlSupplier) GroupDeleteGroupTeam(ctx context.Context, groupID string, teamID string, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	if !model.IsValidId(groupID) {
		result.Err = model.NewAppError("SqlGroupStore.DeleteGroupTeam", "store.sql_group.delete_group_team.group_id.invalid", nil, "group_id="+groupID, http.StatusBadRequest)
	}

	if !model.IsValidId(teamID) {
		result.Err = model.NewAppError("SqlGroupStore.DeleteGroupTeam", "store.sql_group.delete_group_team.team_id.invalid", nil, "group_id="+groupID, http.StatusBadRequest)
	}

	var group *model.Group
	if err := s.GetReplica().SelectOne(&group, "SELECT * from GroupTeams WHERE Id = :Id", map[string]interface{}{"Id": groupID}); err != nil {
		if err == sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete_group_team.app_error", nil, "Id="+groupID+", "+err.Error(), http.StatusNotFound)
		} else {
			result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete_group_team.app_error", nil, err.Error(), http.StatusInternalServerError)
		}

		return result
	}

	if group.DeleteAt != 0 {
		result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete_group_team.app_error", nil, "group_id="+groupID+"team_id="+teamID, http.StatusBadRequest)
	}

	time := model.GetMillis()
	group.DeleteAt = time
	group.UpdateAt = time

	if rowsChanged, err := s.GetMaster().Update(group); err != nil {
		result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete.update.app_error", nil, err.Error(), http.StatusInternalServerError)
	} else if rowsChanged != 1 {
		result.Err = model.NewAppError("SqlGroupStore.Delete", "store.sql_group.delete.update.app_error", nil, "no record to update", http.StatusInternalServerError)
	} else {
		result.Data = group
	}

	return result
}

func (s *SqlSupplier) GroupSaveGroupChannel(ctx context.Context, groupChannel *model.GroupChannel, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()

	if err := groupChannel.IsValid(); err != nil {
		result.Err = err
		return result
	}

	var retrievedGroupChannel *model.GroupChannel
	if err := s.GetMaster().SelectOne(&retrievedGroupChannel, "SELECT * FROM GroupChannels WHERE GroupId = :GroupId AND ChannelId = :ChannelId", map[string]interface{}{"GroupId": groupChannel.GroupId, "ChannelId": groupChannel.ChannelId}); err != nil {
		if err != sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupChannel", "store.sql_group.save_group_channel.app_error", nil, "group_id="+groupChannel.GroupId+"channel_id="+groupChannel.ChannelId+","+err.Error(), http.StatusInternalServerError)
			return result
		}
	}

	groupChannel.UpdateAt = model.GetMillis()

	if retrievedGroupChannel == nil {
		groupChannel.CreateAt = groupChannel.UpdateAt
		if err := s.GetMaster().Insert(groupChannel); err != nil {
			if IsUniqueConstraintError(err, []string{"GroupId", "ChannelId", "groupchannels_pkey", "PRIMARY"}) {
				result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save_group_channel.exists.app_error", nil, "group_id="+groupChannel.GroupId+", channel_id="+groupChannel.ChannelId+", "+err.Error(), http.StatusBadRequest)
				return result
			}
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save_group_channel.save.app_error", nil, "group_id="+groupChannel.GroupId+", channel_id="+groupChannel.ChannelId+", "+err.Error(), http.StatusInternalServerError)
			return result
		}
	} else {
		if rowsChanged, err := s.GetMaster().Update(groupChannel); err != nil {
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save_group_channel.update.app_error", nil, err.Error(), http.StatusInternalServerError)
			return result
		} else if rowsChanged != 1 {
			result.Err = model.NewAppError("SqlGroupStore.SaveGroupTeam", "store.sql_group.save_group_channel.update.app_error", nil, "no record to update", http.StatusInternalServerError)
			return result
		}
	}

	if err := s.GetMaster().SelectOne(&retrievedGroupChannel, "SELECT * FROM GroupChannels WHERE GroupId = :GroupId AND ChannelId = :ChannelId", map[string]interface{}{"GroupId": groupChannel.GroupId, "ChannelId": groupChannel.ChannelId}); err != nil {
		if err != sql.ErrNoRows {
			result.Err = model.NewAppError("SqlGroupStore.CreateMember", "store.sql_group.save_group_channel.app_error", nil, "group_id="+groupChannel.GroupId+"channel_id="+groupChannel.ChannelId+","+err.Error(), http.StatusInternalServerError)
			return result
		}
	}

	result.Data = retrievedGroupChannel
	return result
}

func (s *SqlSupplier) GroupDeleteGroupChannel(ctx context.Context, groupID string, channelID string, hints ...store.LayeredStoreHint) *store.LayeredStoreSupplierResult {
	result := store.NewSupplierResult()
	// TODO
	return result
}
