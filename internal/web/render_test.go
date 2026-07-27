package web

import (
	"bytes"
	"html/template"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/PeterGrunig/Attendance-HackDay/internal/view"
)

func TestRenderedPagesReferenceExistingCSS(t *testing.T) {
	cases := []struct {
		name string
		load func() (*template.Template, error)
	}{
		{
			name: "login",
			load: func() (*template.Template, error) {
				return loadUnAuthTemplates("login.html")
			},
		},
		{
			name: "student dashboard",
			load: func() (*template.Template, error) {
				return loadStudentTemplates("studentDash.html")
			},
		},
		{
			name: "student shop",
			load: func() (*template.Template, error) {
				return loadStudentTemplates("shopView.html")
			},
		},
		{
			name: "student avatar",
			load: func() (*template.Template, error) {
				return loadStudentTemplates("avatarView.html")
			},
		},
		{
			name: "teacher dashboard",
			load: func() (*template.Template, error) {
				return loadTeacherTemplates("teacherDash.html")
			},
		},
		{
			name: "teacher attendance approval",
			load: func() (*template.Template, error) {
				return loadTeacherTemplates("attendanceApproval.html")
			},
		},
		{
			name: "admin dashboard",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("adminDash.html")
			},
		},
		{
			name: "create classroom",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("classrooms.html")
			},
		},
		{
			name: "edit classrooms",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("editClassrooms.html")
			},
		},
		{
			name: "add teacher",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("createTeacher.html")
			},
		},
		{
			name: "add student",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("createStudent.html")
			},
		},
		{
			name: "user settings",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("userSettings.html")
			},
		},
		{
			name: "Canvas integration",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("canvasIntegration.html")
			},
		},
		{
			name: "Canvas import preview",
			load: func() (*template.Template, error) {
				return loadAdminTemplates("canvasPreview.html")
			},
		},
	}

	cssLink := regexp.MustCompile(`<link[^>]+rel="stylesheet"[^>]+href="([^"]+)"`)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := tc.load()
			if err != nil {
				t.Fatalf("load template: %v", err)
			}

			var rendered bytes.Buffer
			if err := tmpl.ExecuteTemplate(&rendered, "base", nil); err != nil {
				t.Fatalf("render template: %v", err)
			}

			matches := cssLink.FindAllStringSubmatch(rendered.String(), -1)
			if len(matches) == 0 {
				t.Fatal("expected at least one stylesheet link")
			}

			for _, match := range matches {
				href := match[1]
				if !strings.HasPrefix(href, "/static/") {
					t.Fatalf("stylesheet %q is not served from /static", href)
				}

				assetPath, _, _ := strings.Cut(strings.TrimPrefix(href, "/"), "?")
				if _, err := fs.Stat(view.FS, assetPath); err != nil {
					t.Fatalf("stylesheet %q does not exist in embedded FS: %v", href, err)
				}
			}
		})
	}
}

func TestTemplatesReferenceExistingStaticAssets(t *testing.T) {
	staticRef := regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`)

	if err := fs.WalkDir(view.FS, ".", func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(filePath, ".html") {
			return nil
		}

		contents, err := fs.ReadFile(view.FS, filePath)
		if err != nil {
			return err
		}

		for _, match := range staticRef.FindAllStringSubmatch(string(contents), -1) {
			assetPath, _, _ := strings.Cut(strings.TrimPrefix(match[1], "/"), "?")
			if _, err := fs.Stat(view.FS, assetPath); err != nil {
				t.Errorf("%s references missing static asset %q: %v", filePath, match[1], err)
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("walk embedded templates: %v", err)
	}
}

func TestStudentShellRendersSidebarControlsAndProofNav(t *testing.T) {
	tmpl, err := loadStudentTemplates("studentDash.html")
	if err != nil {
		t.Fatalf("load student template: %v", err)
	}

	data := PageData{
		Title:             "Student Dashboard",
		Username:          "Seth",
		Coins:             12,
		CanMarkAttendance: true,
		ActiveNav:         "home",
		UseStudentCSS:     true,
		AvatarPreview:     buildAvatarPreview(nil),
		ThemeBackgroundOptions: []ThemeBackgroundOptionView{
			{ID: "beach", Label: "Beach"},
		},
	}

	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "base", data); err != nil {
		t.Fatalf("render student template: %v", err)
	}

	body := rendered.String()
	if count := strings.Count(body, `action="/attendance"`); count != 1 {
		t.Fatalf("attendance form count = %d, want 1", count)
	}
	if strings.Contains(body, `class="bottombar"`) {
		t.Fatal("rendered student shell still includes footer")
	}

	for _, want := range []string{
		`class="student-theme-panel"`,
		`data-sidebar-toggle`,
		`data-bg-value="beach"`,
		`Games`,
		`Social`,
		`Stats`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered student shell missing %q", want)
		}
	}
}

func TestStudentCSSReferencesExistingFontAssets(t *testing.T) {
	contents, err := fs.ReadFile(view.FS, "static/css/student.css")
	if err != nil {
		t.Fatalf("read student css: %v", err)
	}

	fontRef := regexp.MustCompile(`url\("(/static/fonts/[^"]+)"\)`)
	matches := fontRef.FindAllStringSubmatch(string(contents), -1)
	if len(matches) == 0 {
		t.Fatal("expected student css to reference self-hosted fonts")
	}

	for _, match := range matches {
		assetPath := strings.TrimPrefix(match[1], "/")
		if _, err := fs.Stat(view.FS, assetPath); err != nil {
			t.Fatalf("font asset %q does not exist in embedded FS: %v", match[1], err)
		}
	}
}
