package pop

import (
	"github.com/gobuffalo/pop/associations"
	"github.com/gobuffalo/validate"
)

func (c *Connection) eagerCreate(model interface{}, excludeColumns ...string) error {
	asos, err := associations.AssociationsForStruct(model, c.eagerFields...)
	if err != nil {
		return err
	}

	c.eager = false

	if len(asos) == 0 {
		return c.Create(model, excludeColumns...)
	}

	before := asos.AssociationsBeforeCreatable()
	for index := range before {
		i := before[index].BeforeInterface()
		if i == nil {
			continue
		}

		err = c.Create(i)
		if err != nil {
			return err
		}

		err = before[index].BeforeSetup()
		if err != nil {
			return err
		}
	}

	err = c.Create(model, excludeColumns...)
	if err != nil {
		return err
	}

	after := asos.AssociationsAfterCreatable()
	for index := range after {
		err = after[index].AfterSetup()
		if err != nil {
			return err
		}

		i := after[index].AfterInterface()
		if i == nil {
			continue
		}

		err = c.Create(i)
		if err != nil {
			return err
		}
	}

	stms := asos.AssociationsCreatableStatement()
	for index := range stms {
		statements := stms[index].Statements()
		for _, stm := range statements {
			_, err = c.TX.Exec(c.Dialect.TranslateSQL(stm.Statement), stm.Args...)
			if err != nil {
				return err
			}
		}
	}

	return err
}

func (c *Connection) eagerValidateAndCreate(model interface{}, excludeColumns ...string) (*validate.Errors, error) {
	asos, err := associations.AssociationsForStruct(model, c.eagerFields...)
	verrs := validate.NewErrors()

	if err != nil {
		return verrs, err
	}

	if len(asos) == 0 {
		c.eager = false
		return c.ValidateAndCreate(model, excludeColumns...)
	}

	before := asos.AssociationsBeforeCreatable()
	for index := range before {
		i := before[index].BeforeInterface()
		if i == nil {
			continue
		}

		sm := &Model{Value: i}
		verrs, err := sm.validateCreate(c)
		if err != nil || verrs.HasAny() {
			return verrs, err
		}
	}

	after := asos.AssociationsAfterCreatable()
	for index := range after {
		i := after[index].AfterInterface()
		if i == nil {
			continue
		}

		sm := &Model{Value: i}
		verrs, err := sm.validateCreate(c)
		if err != nil || verrs.HasAny() {
			return verrs, err
		}
	}

	return verrs, c.eagerCreate(model, excludeColumns...)
}
