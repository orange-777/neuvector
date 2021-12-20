package cvetools

import (
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/neuvector/neuvector/scanner/common"
	"github.com/neuvector/neuvector/share/scan"
)

func (cv *CveTools) DetectAppVul(path string, appPkg []scan.AppPackage, namespace string) []vulFullReport {
	if appPkg == nil || len(appPkg) == 0 {
		return nil
	}
	modVuls, err := common.LoadAppVulsTb(path)
	if err != nil {
		return nil
	}

	vuls := make([]vulFullReport, 0)

	for _, pkg := range appPkg {
		// log.WithFields(log.Fields{"namespace": namespace, "package": pkg}).Info()

		// It seems that alpine patches python vulnerabilities actively. Even the library
		// version is vulnerable, the source codes are patched.
		if strings.HasPrefix(namespace, "alpine:") && strings.HasPrefix(pkg.ModuleName, "python:") {
			continue
		}

		if mv, found := modVuls[pkg.ModuleName]; found {
			for _, v := range mv {
				if len(v.UnaffectedVer) > 0 {
					if unaffected := compareAppVersion(pkg.Version, v.UnaffectedVer); unaffected {
						continue
					}
				}
				// ruby reports patched version. The affected version is converted from patched version.
				// The conversion logic is not correct.
				if strings.HasPrefix(pkg.ModuleName, "ruby:") && len(v.FixedVer) > 0 {
					if fixed := compareAppVersion(pkg.Version, v.FixedVer); !fixed {
						fv := appVul2FullVul(pkg, v)
						vuls = append(vuls, fv)
					}
				} else {
					if affected := compareAppVersion(pkg.Version, v.AffectedVer); affected {
						fv := appVul2FullVul(pkg, v)
						vuls = append(vuls, fv)
					}
				}
			}
		}
	}

	return vuls
}

func appVul2FullVul(pkg scan.AppPackage, mv common.AppModuleVul) vulFullReport {
	var fv vulFullReport
	fv.Vf.Name = mv.VulName
	fv.Vf.Namespace = pkg.AppName
	fv.Vf.Description = mv.Description
	fv.Vf.Link = mv.Link
	fv.Vf.Severity = mv.Severity
	fv.Vf.FixedIn = make([]common.FeaFull, 0)
	fv.Vf.FixedIn = append(fv.Vf.FixedIn, moduleVer2FixVer(pkg, mv))
	fv.Vf.Metadata = make(map[string]common.NVDMetadata)

	if strings.HasSuffix(pkg.FileName, scan.WPVerFileSuffix) {
		fv.Ft.Feature.Name = "WordPress"
	} else {
		fv.Ft.Feature.Name = pkg.FileName
	}
	fv.Ft.Feature.Namespace.Name = pkg.AppName
	fv.Ft.Version, _ = common.NewVersion(pkg.Version)
	fv.Ft.InBase = pkg.InBase

	var nv common.NVDMetadata
	nv.CVSSv2.Score = mv.Score
	fv.Vf.Metadata["NVD"] = nv

	return fv
}

func moduleVer2FixVer(pkg scan.AppPackage, mv common.AppModuleVul) common.FeaFull {
	ft := common.FeaFull{Name: mv.ModuleName, Namespace: pkg.AppName}
	for i, v := range mv.FixedVer {
		s := strings.Replace(v.OpCode, "or", "||", -1)
		s = strings.Replace(s, "gt", ">", -1)
		s = strings.Replace(s, "lt", "<", -1)
		s = strings.Replace(s, "eq", "=", -1)
		ft.Version += s + v.Version
		if i < (len(mv.FixedVer) - 1) {
			ft.Version += ";"
		}
	}
	return ft
}

func compareAppVersion(ver string, affectedVer []common.AppModuleVersion) bool {
	// NVSHAS-4684, version in database does have revision
	/*
		//skip the revision, no revision in database
		if a := strings.Index(ver, "-"); a > 0 {
			ver = ver[:a]
		}
	*/
	var bv common.Version
	av, err := common.NewVersion(ver)
	if err != nil {
		log.WithFields(log.Fields{"error": err, "version": ver}).Error("Failed to parse app version")
		return false
	}
	var hit0, hit1 bool
	var lastOp string
	for id, mv := range affectedVer {
		var prefix string
		if mv.Version == "All" {
			return true
		} else {
			// get the prefix out, only for jar
			if a := strings.Index(mv.Version, ","); a > 0 {
				prefix = mv.Version[a+1:]
				mv.Version = mv.Version[:a]
			}
			bv, err = common.NewVersion(mv.Version)
			if err != nil {
				log.WithFields(log.Fields{"error": err, "version": mv.Version}).Error("Failed to parse affected version")
				continue
			}
		}

		if prefix != "" && !strings.HasPrefix(ver, prefix) {
			continue
		}

		ret := av.Compare(bv)

		hit1 = hit0
		hit0 = false
		if mv.OpCode == "" && ret == 0 {
			return true
		} else if strings.Contains(mv.OpCode, "lteq") && ret <= 0 {
			hit0 = true
		} else if strings.Contains(mv.OpCode, "lt") && ret < 0 {
			hit0 = true
		} else if strings.Contains(mv.OpCode, "eq") && ret == 0 {
			hit0 = true
		} else if strings.Contains(mv.OpCode, "gteq") && ret >= 0 {
			hit0 = true
		} else if strings.Contains(mv.OpCode, "gt") && ret > 0 {
			hit0 = true
		}
		// avoid the >=2.7.1,2 and <=2.1,2 case
		if prefix != "" {
			if hit0 && !strings.Contains(mv.OpCode, "gt") && !strings.Contains(lastOp, "gt") {
				return true
			} else {
				return hit0
			}
		}
		//the case with <= || >= <=
		if strings.Contains(mv.OpCode, "or") {
			//in case of pairs: (>= && <=) or (>= && <=)
			if hit1 && !strings.Contains(lastOp, "lt") {
				return true
			} else if hit1 && id == 1 {
				// the case for: (<) || (> && <)
				return true
			} else if hit0 && id == (len(affectedVer)-1) {
				//the last one
				return true
			}
		} else { //the case >= && <=
			if hit1 && hit0 {
				return true
			} else if hit0 && len(affectedVer) == 1 {
				//the last one
				return true
			}
		}
		lastOp = mv.OpCode
	}
	return false
}
