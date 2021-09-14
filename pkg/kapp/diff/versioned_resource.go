// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package diff

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	ctlconf "github.com/k14s/kapp/pkg/kapp/config"
	ctlres "github.com/k14s/kapp/pkg/kapp/resources"
	corev1 "k8s.io/api/core/v1"
)

const (
	nameSuffixSep = "-ver-"
)

type VersionedResource struct {
	res      ctlres.Resource
	allRules []ctlconf.TemplateRule
}

func (d VersionedResource) SetBaseName(ver int) {
	name := fmt.Sprintf("%s%s%d", d.res.Name(), nameSuffixSep, ver)
	d.res.SetName(name)
}

func (d VersionedResource) BaseNameAndVersion() (string, string) {
	name := d.res.Name()
	pieces := strings.Split(name, nameSuffixSep)
	if len(pieces) > 1 {
		return strings.Join(pieces[0:len(pieces)-1], nameSuffixSep), pieces[len(pieces)-1]
	}
	return name, ""
}

func (d VersionedResource) Version() int {
	_, ver := d.BaseNameAndVersion()
	if len(ver) == 0 {
		panic(fmt.Sprintf("Missing version in versioned resource '%s'", d.res.Description()))
	}

	verInt, err1 := strconv.Atoi(ver)
	if err1 != nil {
		panic(fmt.Sprintf("Invalid version in versioned resource '%s'", d.res.Description()))
	}

	return verInt
}

func (d VersionedResource) UniqVersionedKey() ctlres.UniqueResourceKey {
	baseName, _ := d.BaseNameAndVersion()
	return ctlres.NewUniqueResourceKeyWithCustomName(d.res, baseName)
}

func (d VersionedResource) UpdateAffected(rs []ctlres.Resource) error {
	rules, err := d.matchingRules()
	if err != nil {
		return err
	}

	for _, rule := range rules {
		// TODO versioned resources that affect other versioned resources
		err = d.updateAffected(rule, rs)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d VersionedResource) updateAffected(rule ctlconf.TemplateRule, rs []ctlres.Resource) error {
	for _, affectedObjRef := range rule.AffectedResources.ObjectReferences {
		matchers := ctlconf.ResourceMatchers(affectedObjRef.ResourceMatchers).AsResourceMatchers()

		mod := ctlres.ObjectRefSetMod{
			ResourceMatcher: ctlres.AnyMatcher{matchers},
			Path:            affectedObjRef.Path,
			ReplacementFunc: d.buildObjRefReplacementFunc(affectedObjRef),
		}

		for _, res := range rs {
			err := mod.Apply(res)
			if err != nil {
				return err
			}

			if val, found := res.Annotations()[explicitReferenceKey]; found {
				annotation := ExplicitVersionedRefAnn{}

				err := json.Unmarshal([]byte(val), &annotation)
				if err != nil {
					return fmt.Errorf("Error unmarshalling explicit references : %s", err)
				}

				isTarget, err := NewExplicitVersionedRef(d, annotation).IsReferenced()
				if err != nil {
					return err
				}

				if isTarget {
					if annotation.VersionedNames == nil {
						annotation.VersionedNames = map[string]string{}
					}

					annotation.VersionedNames[d.UniqVersionedKey().String()] = d.res.Name()

					out, err := json.Marshal(annotation)
					if err != nil {
						return fmt.Errorf("Error marshalling reference annotation: %s", err)
					}

					err = d.annotationMod(string(out)).Apply(res)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

func (d VersionedResource) annotationMod(annotation string) ctlres.StringMapAppendMod {
	return ctlres.StringMapAppendMod{
		ResourceMatcher: ctlres.AllMatcher{},
		Path:            ctlres.NewPathFromStrings([]string{"metadata", "annotations"}),
		KVs: map[string]string{
			explicitReferenceKey: annotation,
		},
	}
}

func (d VersionedResource) buildObjRefReplacementFunc(
	affectedObjRef ctlconf.TemplateAffectedObjRef) func(map[string]interface{}) error {

	baseName, _ := d.BaseNameAndVersion()

	return func(typedObj map[string]interface{}) error {
		bs, err := json.Marshal(typedObj)
		if err != nil {
			return fmt.Errorf("Remarshaling object reference: %s", err)
		}

		var objRef corev1.ObjectReference

		err = json.Unmarshal(bs, &objRef)
		if err != nil {
			return fmt.Errorf("Unmarshaling object reference: %s", err)
		}

		// Check as many rules as possible
		if len(affectedObjRef.NameKey) > 0 {
			if typedObj[affectedObjRef.NameKey] != baseName {
				return nil
			}
		} else {
			if objRef.Name != baseName {
				return nil
			}
		}

		if len(objRef.Namespace) > 0 && objRef.Namespace != d.res.Namespace() {
			return nil
		}
		if len(objRef.Kind) > 0 && objRef.Kind != d.res.Kind() {
			return nil
		}
		if len(objRef.APIVersion) > 0 && objRef.APIVersion != d.res.APIVersion() {
			return nil
		}

		if len(affectedObjRef.NameKey) > 0 {
			typedObj[affectedObjRef.NameKey] = d.res.Name()
		} else {
			typedObj["name"] = d.res.Name()
		}

		return nil
	}
}

func (d VersionedResource) matchingRules() ([]ctlconf.TemplateRule, error) {
	var result []ctlconf.TemplateRule

	for _, rule := range d.allRules {
		matchers := ctlconf.ResourceMatchers(rule.ResourceMatchers).AsResourceMatchers()
		if (ctlres.AnyMatcher{matchers}).Matches(d.res) {
			result = append(result, rule)
		}
	}

	return result, nil
}
