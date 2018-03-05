package pop

import (
	"fmt"
	"reflect"

	"github.com/markbates/pop/associations"

	"github.com/gobuffalo/pop/columns"
	"github.com/gobuffalo/uuid"
	"github.com/gobuffalo/validate"
)

// Reload fetch fresh data for a given model, using its ID
func (c *Connection) Reload(model interface{}) error {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			err = c.Reload(val.Interface())
			if err != nil {
				return err
			}
		}
		return err
	}

	sm := Model{Value: model}
	return c.Find(model, sm.ID())
}

// Exec runs the given query
func (q *Query) Exec() error {
	return q.Connection.timeFunc("Exec", func() error {
		sql, args := q.ToSQL(nil)
		Log(sql, args...)
		_, err := q.Connection.Store.Exec(sql, args...)
		return err
	})
}

func (q *Query) ExecWithCount() (int, error) {
	count := int64(0)
	return int(count), q.Connection.timeFunc("Exec", func() error {
		sql, args := q.ToSQL(nil)
		Log(sql, args...)
		result, err := q.Connection.Store.Exec(sql, args...)
		if err != nil {
			return err
		}

		count, err = result.RowsAffected()
		return err
	})
}

// ValidateAndSave applies validation rules on the given entry, then save it
// if the validation succeed, excluding the given columns.
func (c *Connection) ValidateAndSave(model interface{}, excludeColumns ...string) (*validate.Errors, error) {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			verrs, err := c.ValidateAndSave(val.Interface(), excludeColumns...)
			if err != nil {
				return verrs, err
			}
			if verrs.HasAny() {
				return verrs, nil
			}
		}
		return validate.NewErrors(), err
	}

	sm := &Model{Value: model}
	verrs, err := sm.validateSave(c)
	if err != nil {
		return verrs, err
	}
	if verrs.HasAny() {
		return verrs, nil
	}
	return verrs, c.Save(model, excludeColumns...)
}

var emptyUUID = uuid.Nil.String()

// Save wraps the Create and Update methods. It executes a Create if no ID is provided with the entry;
// or issues an Update otherwise.
func (c *Connection) Save(model interface{}, excludeColumns ...string) error {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			err = c.Save(val.Interface(), excludeColumns...)
			if err != nil {
				return err
			}
		}
		return err
	}

	sm := &Model{Value: model}
	id := sm.ID()

	if fmt.Sprint(id) == "0" || fmt.Sprint(id) == emptyUUID {
		return c.Create(model, excludeColumns...)
	}
	return c.Update(model, excludeColumns...)
}

// ValidateAndCreate applies validation rules on the given entry, then creates it
// if the validation succeed, excluding the given columns.
func (c *Connection) ValidateAndCreate(model interface{}, excludeColumns ...string) (*validate.Errors, error) {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			verrs, err := c.ValidateAndCreate(val.Interface(), excludeColumns...)
			if err != nil {
				return verrs, err
			}
			if verrs.HasAny() {
				return verrs, nil
			}
		}
		return validate.NewErrors(), err
	}

	sm := &Model{Value: model}
	verrs, err := sm.validateCreate(c)
	if err != nil {
		return verrs, err
	}
	if verrs.HasAny() {
		return verrs, nil
	}
	return verrs, c.Create(model, excludeColumns...)
}

// Create add a new given entry to the database, excluding the given columns.
// It updates `created_at` and `updated_at` columns automatically.
func (c *Connection) Create(model interface{}, excludeColumns ...string) error {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			err = c.Create(val.Interface(), excludeColumns...)
			if err != nil {
				return err
			}
		}
		return err
	}

	if !c.eager {
		return c.createOne(model, excludeColumns...)
	}

	asos, err := associations.AssociationsForStruct(model, c.eagerFields...)
	if err != nil {
		return err
	}

	c.eager = false
	for _, a := range asos {
		// Create all dependencies first.
		dependencies := a.Dependencies()
		for _, d := range dependencies {
			err = c.Create(d)
			if err != nil {
				return err
			}
		}

		// set values based on dependencies.
		a.SetValue(dependencies)

		if acs, ok := a.(associations.AssociationCreatableStatement); ok {
			stms := acs.Statements()
			for _, stm := range stms {
				_, err = c.TX.Exec(c.Dialect.TranslateSQL(stm.Statement), stm.Args...)
				if err != nil {
					return err
				}
			}
			continue
		}

		err = c.Create(a.Interface())
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Connection) createOne(model interface{}, excludeColumns ...string) error {
	return c.timeFunc("Create", func() error {
		var err error
		sm := &Model{Value: model}

		if err = sm.beforeSave(c); err != nil {
			return err
		}

		if err = sm.beforeCreate(c); err != nil {
			return err
		}

		cols := columns.ColumnsForStructWithAlias(model, sm.TableName(), sm.As)
		cols.Remove(excludeColumns...)

		sm.touchCreatedAt()
		sm.touchUpdatedAt()

		if err = c.Dialect.Create(c.Store, sm, cols); err != nil {
			return err
		}

		if err = sm.afterCreate(c); err != nil {
			return err
		}

		return sm.afterSave(c)
	})
}

// ValidateAndUpdate applies validation rules on the given entry, then update it
// if the validation succeed, excluding the given columns.
func (c *Connection) ValidateAndUpdate(model interface{}, excludeColumns ...string) (*validate.Errors, error) {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			verrs, err := c.ValidateAndUpdate(val.Interface(), excludeColumns...)
			if err != nil {
				return verrs, err
			}
			if verrs.HasAny() {
				return verrs, nil
			}
		}
		return validate.NewErrors(), err
	}

	sm := &Model{Value: model}
	verrs, err := sm.validateUpdate(c)
	if err != nil {
		return verrs, err
	}
	if verrs.HasAny() {
		return verrs, nil
	}
	return verrs, c.Update(model, excludeColumns...)
}

// Update writes changes from an entry to the database, excluding the given columns.
// It updates the `updated_at` column automatically.
func (c *Connection) Update(model interface{}, excludeColumns ...string) error {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			err = c.Update(val.Interface(), excludeColumns...)
			if err != nil {
				return err
			}
		}
		return err
	}

	return c.timeFunc("Update", func() error {
		var err error
		sm := &Model{Value: model}

		if err = sm.beforeSave(c); err != nil {
			return err
		}
		if err = sm.beforeUpdate(c); err != nil {
			return err
		}

		cols := columns.ColumnsForStructWithAlias(model, sm.TableName(), sm.As)
		cols.Remove("id", "created_at")
		cols.Remove(excludeColumns...)

		sm.touchUpdatedAt()

		if err = c.Dialect.Update(c.Store, sm, cols); err != nil {
			return err
		}
		if err = sm.afterUpdate(c); err != nil {
			return err
		}

		return sm.afterSave(c)
	})
}

// Destroy deletes a given entry from the database
func (c *Connection) Destroy(model interface{}) error {
	v := reflect.Indirect(reflect.ValueOf(model))
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		var err error
		for i := 0; i < v.Len(); i++ {
			val := v.Index(i)
			if val.Kind() != reflect.Ptr {
				val = val.Addr()
			}
			err = c.Destroy(val.Interface())
			if err != nil {
				return err
			}
		}
		return err
	}

	return c.timeFunc("Destroy", func() error {
		var err error
		sm := &Model{Value: model}

		if err = sm.beforeDestroy(c); err != nil {
			return err
		}
		if err = c.Dialect.Destroy(c.Store, sm); err != nil {
			return err
		}

		return sm.afterDestroy(c)
	})
}
