package rancher

import (
	"fmt"
	"strings"
)

//supported protocols
const (
	NEQ   = "!="
	EQ    = "="
	NOTIN = " notin "
	IN    = " in "
	NOOP  = ""
)

type SelectorConstraint interface {
	IsSelectorMatch(labels map[string]string) bool
}

type SelectorConstraintIn struct {
	Key   string
	Value []string
}

func (s SelectorConstraintIn) IsSelectorMatch(labels map[string]string) bool {
	var found bool
	for k := range labels {
		if strings.EqualFold(k, s.Key) {
			for _, v := range s.Value {
				if strings.EqualFold(labels[k], v) {
					found = true
					break
				}
			}
		}
	}
	return found
}

type SelectorConstraintNotIn struct {
	Key   string
	Value []string
}

func (s SelectorConstraintNotIn) IsSelectorMatch(labels map[string]string) bool {
	var found bool
	for k := range labels {
		if strings.EqualFold(k, s.Key) {
			for _, v := range s.Value {
				if !strings.EqualFold(labels[k], v) {
					found = true
					break
				}
			}
		}
	}
	return found
}

type SelectorConstraintEq struct {
	Key   string
	Value string
}

func (s SelectorConstraintEq) IsSelectorMatch(labels map[string]string) bool {
	var found bool
	for k := range labels {
		if strings.EqualFold(k, s.Key) {
			if strings.EqualFold(labels[k], s.Value) {
				found = true
				break
			}
		}
	}
	return found
}

type SelectorConstraintNEq struct {
	Key   string
	Value string
}

func (s SelectorConstraintNEq) IsSelectorMatch(labels map[string]string) bool {
	var found bool
	for k := range labels {
		if strings.EqualFold(k, s.Key) {
			if !strings.EqualFold(labels[k], s.Value) {
				found = true
				break
			}
		}
	}
	return found
}

type SelectorConstraintNoop struct {
	Key string
}

func (s SelectorConstraintNoop) IsSelectorMatch(labels map[string]string) bool {
	_, ok := labels[s.Key]
	return ok
}

func IsSelectorMatch(selector string, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}

	if labels == nil {
		labels = map[string]string{}
	}

	constraints := GetSelectorConstraints(selector)
	if len(constraints) == 0 {
		return false
	}

	found := 0
	for _, constraint := range constraints {
		if constraint.IsSelectorMatch(labels) {
			found = found + 1
		}
	}
	if found != len(constraints) {
		return false
	}
	return true
}

func GetSelectorConstraints(selector string) []SelectorConstraint {
	var constraints []SelectorConstraint
	var constraintsStr []string
	inList := false
	constraint := ""
	for i, char := range selector {
		c := string(char)
		finishConstraint := (i == len(selector)-1)
		if c == "(" {
			inList = true
		} else if c == ")" {
			inList = false
		} else if c == "," {
			if inList {
				constraint = fmt.Sprintf("%s%s", constraint, c)
			} else {
				finishConstraint = true
			}
		} else {
			constraint = fmt.Sprintf("%s%s", constraint, c)
		}
		if finishConstraint {
			constraintsStr = append(constraintsStr, constraint)
			constraint = ""
		}
	}
	for _, constraintStr := range constraintsStr {
		c := GetSelectorConstraint(constraintStr)
		if c == nil {
			continue
		}
		constraints = append(constraints, c)
	}
	return constraints
}

func GetSelectorConstraint(selector string) SelectorConstraint {
	var ops []string
	opts := append(ops, NOOP, EQ, NOTIN, IN, NEQ)
	finalOp := NOOP
	key := ""
	value := ""
	for _, op := range opts {
		if op == NOOP {
			continue
		}
		exp := strings.Split(selector, op)
		if len(exp) == 2 {
			finalOp = op
			key = strings.TrimSpace(exp[0])
			value = strings.TrimSpace(exp[1])
		}
		if finalOp == EQ {
			return &SelectorConstraintEq{
				Key:   key,
				Value: value,
			}
		} else if finalOp == EQ {
			return &SelectorConstraintNEq{
				Key:   key,
				Value: value,
			}
		} else if finalOp == NOOP {
			return &SelectorConstraintNoop{
				Key: key,
			}
		} else if finalOp == IN {
			return &SelectorConstraintIn{
				Key:   key,
				Value: strings.Split(value, ","),
			}
		} else if finalOp == NOTIN {
			return &SelectorConstraintNotIn{
				Key:   key,
				Value: strings.Split(value, ","),
			}
		}
	}
	return nil
}
