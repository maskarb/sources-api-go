package dao

import (
	"errors"
	"fmt"

	"github.com/RedHatInsights/sources-api-go/dao/mappers"
	m "github.com/RedHatInsights/sources-api-go/model"
	"github.com/RedHatInsights/sources-api-go/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RhcConnectionDaoImpl struct {
	TenantID int64
}

func (s *RhcConnectionDaoImpl) List(limit, offset int, filters []util.Filter) ([]m.RhcConnection, int64, error) {
	query := DB.
		Debug().
		Model(&m.RhcConnection{}).
		Select(`"rhc_connections".*, STRING_AGG(CAST ("jt"."source_id" AS TEXT), ',') AS "source_ids"`).
		Joins(`INNER JOIN "source_rhc_connections" AS "jt" ON "rhc_connections"."id" = "jt"."rhc_connection_id"`).
		Where(`"jt"."tenant_id" = ?`, s.TenantID).
		Group(`"rhc_connections"."id"`).
		Limit(limit).
		Offset(offset)

	query, err := applyFilters(query, filters)
	if err != nil {
		return nil, 0, err
	}

	// Run the actual query.
	result, err := query.Rows()
	if err != nil {
		return nil, 0, err
	}

	// We call next as otherwise "ScanRows" complains, but since we're going to map the results to an array of
	// map[string]interface{}, "ScanRows" will already scan every row into that array, thus freeing us from calling
	// result.Next() again.
	if !result.Next() {
		return []m.RhcConnection{}, 0, nil
	}

	// Loop through the rows to map both the connection and its related sources.
	var rows []map[string]interface{}
	err = DB.ScanRows(result, &rows)
	if err != nil {
		return nil, 0, err
	}

	rhcConnections := make([]m.RhcConnection, 0)
	for _, row := range rows {
		rhcConnection, err := mappers.MapRowToRhcConnection(row)
		if err != nil {
			return nil, 0, err
		}

		rhcConnections = append(rhcConnections, *rhcConnection)
	}

	err = result.Close()
	if err != nil {
		return nil, 0, err
	}

	// Getting the total count (filters included) for pagination.
	count := int64(0)
	query.Count(&count)

	return rhcConnections, count, nil
}

func (s *RhcConnectionDaoImpl) GetById(id *int64) (*m.RhcConnection, error) {
	query := DB.
		Debug().
		Model(&m.RhcConnection{}).
		Select(`"rhc_connections".*, STRING_AGG(CAST ("jt"."source_id" AS TEXT), ',') AS "source_ids"`).
		Joins(`INNER JOIN "source_rhc_connections" AS "jt" ON "rhc_connections"."id" = "jt"."rhc_connection_id"`).
		Where(`"rhc_connections"."id" = ?`, id).
		Where(`"jt"."tenant_id" = ?`, s.TenantID).
		Group(`"rhc_connections"."id"`)

	// Run the actual query.
	result, err := query.Rows()
	if err != nil {
		return nil, err
	}

	// We call next as otherwise "ScanRows" complains, but since we're going to map the results to an array of
	// map[string]interface{}, "ScanRows" will already scan every row into that array, thus freeing us from calling
	// result.Next() again.
	if !result.Next() {
		return nil, util.NewErrNotFound("rhcConnection")
	}

	// Loop through the rows to map both the connection and its related sources.
	var rows []map[string]interface{}
	err = DB.ScanRows(result, &rows)
	if err != nil {
		return nil, err
	}

	err = result.Close()
	if err != nil {
		return nil, err
	}

	if len(rows) != 1 {
		return nil, errors.New("unexpected number of results")
	}

	rhcConnection, err := mappers.MapRowToRhcConnection(rows[0])
	if err != nil {
		return nil, err
	}

	return rhcConnection, nil
}

func (s *RhcConnectionDaoImpl) Create(rhcConnection *m.RhcConnection) (*m.RhcConnection, error) {
	// If the source doesn't exist we cannot create the RhcConnection, since it needs to be linked to at least one
	// source.
	var sourceExists bool
	err := DB.Debug().
		Model(&m.Source{}).
		Select(`1`).
		Where(`id = ?`, rhcConnection.Sources[0].ID).
		Where(`tenant_id = ?`, s.TenantID).
		Scan(&sourceExists).
		Error

	// Something went wrong with the query
	if err != nil {
		return nil, err
	}

	if !sourceExists {
		return nil, util.NewErrNotFound("source")
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		var err error

		err = tx.Debug().
			Where(`rhc_id = ?`, rhcConnection.RhcId).
			Omit(clause.Associations).
			FirstOrCreate(&rhcConnection).
			Error

		if err != nil {
			return err
		}

		// Try to insert an sourceRhcConnection. If it exists the database will complain.
		sourceRhcConnection := m.SourceRhcConnection{
			SourceId:        rhcConnection.Sources[0].ID,
			RhcConnectionId: rhcConnection.ID,
			TenantId:        s.TenantID,
		}

		err = tx.Debug().
			Create(&sourceRhcConnection).
			Error

		if err != nil {
			return fmt.Errorf("cannot link red hat connection to source: %w", err)
		}

		return nil
	})

	return rhcConnection, err
}

func (s *RhcConnectionDaoImpl) Update(rhcConnection *m.RhcConnection) error {
	err := DB.Debug().
		Updates(rhcConnection).
		Error
	return err
}

func (s *RhcConnectionDaoImpl) Delete(id *int64) (*m.RhcConnection, error) {
	var rhcConnection m.RhcConnection

	err := DB.Debug().
		Where("id = ?", id).
		First(&rhcConnection).
		Error

	if err != nil {
		return nil, util.NewErrNotFound("rhcConnection")
	}

	// The foreign key in the join table takes care of deleting the associated row.
	err = DB.Debug().
		Where(`id = ?`, *id).
		Delete(&m.RhcConnection{}).
		Error

	return &rhcConnection, err
}

func (s *RhcConnectionDaoImpl) ListForSource(sourceId *int64, limit, offset int, filters []util.Filter) ([]m.RhcConnection, int64, error) {
	rhcConnections := make([]m.RhcConnection, 0)

	query := DB.Debug().
		Model(&m.RhcConnection{}).
		Joins(`INNER JOIN "source_rhc_connections" "sr" ON "rhc_connections"."id" = "sr"."rhc_connection_id"`).
		Where(`"sr"."source_id" = ?`, sourceId).
		Where(`"sr"."tenant_id" = ?`, s.TenantID).
		Limit(limit).
		Offset(offset)

	query, err := applyFilters(query, filters)
	if err != nil {
		return nil, 0, err
	}

	// Getting the total count (filters included) for pagination.
	count := int64(0)
	query.Count(&count)

	// Run the actual query.
	err = query.Find(&rhcConnections).Error

	return rhcConnections, count, err

}