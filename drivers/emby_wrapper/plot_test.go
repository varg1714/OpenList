package emby_wrapper_test

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// TestPlotConfigured：plot 单独配置即生成 nfo，plot 出现在内容中。
func TestPlotConfigured(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"测试简介"}`); err != nil {
		t.Fatalf("set plot: %+v", err)
	}
	objs, err := d.List(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, model.ListArgs{Refresh: true})
	if err != nil {
		t.Fatalf("list: %+v", err)
	}
	if got := names(objs); len(got) != 2 {
		t.Fatalf("expected [AAA.mkv AAA.nfo], got %v", got)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<title><![CDATA[测试简介]]></title>") {
		t.Errorf("title must use plot value, got %s", got)
	}
	if !strings.Contains(got, "<plot><![CDATA[测试简介]]></plot>") {
		t.Errorf("nfo must contain plot, got %s", got)
	}
}

// TestPlotAppendFileName：append 开启，plot = plot + '-' + 去扩展名文件名。
func TestPlotAppendFileName(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"P","append_file_name_to_plot":true}`); err != nil {
		t.Fatalf("set plot+append: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<title><![CDATA[P-AAA]]></title>") {
		t.Errorf("title must be P-AAA, got %s", got)
	}
	if !strings.Contains(got, "<plot><![CDATA[P-AAA]]></plot>") {
		t.Errorf("expected plot P-AAA, got %s", got)
	}
}

// TestPlotAppendWithoutPlot：append 开启但 plot 未设置，plot = 文件名本身。
func TestPlotAppendWithoutPlot(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"append_file_name_to_plot":true}`); err != nil {
		t.Fatalf("set append: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<title><![CDATA[AAA]]></title>") {
		t.Errorf("title must stay file name AAA, got %s", got)
	}
	if !strings.Contains(got, "<plot><![CDATA[AAA]]></plot>") {
		t.Errorf("expected plot AAA, got %s", got)
	}
}

// TestPlotAppendDisabled：append 未开启，plot 原样。
func TestPlotAppendDisabled(t *testing.T) {
	d := setup(t)
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"P"}`); err != nil {
		t.Fatalf("set plot: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/AAA.nfo")
	if !strings.Contains(got, "<title><![CDATA[P]]></title>") {
		t.Errorf("title must be P, got %s", got)
	}
	if !strings.Contains(got, "<plot><![CDATA[P]]></plot>") {
		t.Errorf("expected plot P, got %s", got)
	}
	if strings.Contains(got, "P-AAA") {
		t.Errorf("append must be off by default, got %s", got)
	}
}

// TestPlotInheritedDimension：plot 分维度继承到子文件夹，actors 用近处值。
func TestPlotInheritedDimension(t *testing.T) {
	d := setup(t)
	if err := writeDownstreamDir(t, "/Movies/A1"); err != nil {
		t.Fatalf("mkdir A1: %v", err)
	}
	if err := writeDownstreamFile(t, "/Movies/A1/BBB.mp4", "x"); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := d.Rename(context.Background(), &model.Object{Name: "Movies", Path: "/Movies", IsFolder: true}, `{"plot":"P","append_file_name_to_plot":true}`); err != nil {
		t.Fatalf("config Movies: %+v", err)
	}
	// A1 手动设 actors，plot 仍继承
	if err := d.Rename(context.Background(), &model.Object{Name: "A1", Path: "/Movies/A1", IsFolder: true}, `{"actors":"Y"}`); err != nil {
		t.Fatalf("set actors on A1: %+v", err)
	}
	got := readNFOLink(t, d, "/Movies/A1/BBB.nfo")
	if !strings.Contains(got, "<title><![CDATA[P-BBB]]></title>") {
		t.Errorf("title must inherit and append, got %s", got)
	}
	if !strings.Contains(got, "<plot><![CDATA[P-BBB]]></plot>") {
		t.Errorf("plot must inherit and append, got %s", got)
	}
	if !strings.Contains(got, "<name>Y</name>") {
		t.Errorf("actors must use near value Y, got %s", got)
	}
}
