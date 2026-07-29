package web

import (
	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	"github.com/PeterGrunig/Attendance-HackDay/internal/viewmodel"
)

type User = domain.User
type Classroom = domain.Classroom
type Schedule = domain.Schedule
type ShopItem = domain.ShopItem
type AvatarConfig = domain.AvatarConfig
type CoinTransaction = domain.CoinTransaction
type AttendanceRecord = domain.AttendanceRecord

type PageData = viewmodel.PageData
type DoubleDayView = viewmodel.DoubleDayView
type ShopItemView = viewmodel.ShopItemView
type ThemeBackgroundOptionView = viewmodel.ThemeBackgroundOptionView
type AvatarBaseOptionView = viewmodel.AvatarBaseOptionView
type AvatarCosmeticOptionView = viewmodel.AvatarCosmeticOptionView
type AvatarLayerView = viewmodel.AvatarLayerView
type AvatarPreviewView = viewmodel.AvatarPreviewView
