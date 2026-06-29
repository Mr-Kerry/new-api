package model

type ChannelMonitorLog struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	ChannelId    int    `json:"channel_id" gorm:"index;not null"`
	ChannelType  int    `json:"channel_type" gorm:"index"`
	ChannelName  string `json:"channel_name" gorm:"size:100;index"`
	Source       string `json:"source" gorm:"size:32;index"` // scheduled/manual_single/manual_bulk
	TaskId       string `json:"task_id" gorm:"size:64;index"`
	KeyIndex     int    `json:"key_index" gorm:"index"`
	Success      bool   `json:"success" gorm:"index"`
	ResponseTime int    `json:"response_time"`
	ErrorCode    string `json:"error_code" gorm:"size:64"`
	ErrorMessage string `json:"error_message" gorm:"type:text"`
	StatusBefore int    `json:"status_before" gorm:"index"`
	StatusAfter  int    `json:"status_after" gorm:"index"`
	CreatedTime  int64  `json:"created_time" gorm:"index"`
}