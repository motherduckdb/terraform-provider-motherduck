package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

// flightLogsQuery reads a window of Flight run logs. MotherDuck applies limit,
// offset, and order on the server, so a large log never crosses the wire in
// full. Without a limit MotherDuck returns only the most recent lines, and
// it rejects a zero limit and an offset without a limit, so those cases are
// translated here instead of failing the read.
func flightLogsQuery(m rowsModel) (string, error) {
	if m.FlightID.IsNull() || m.RunNumber.IsNull() {
		return "", fmt.Errorf("flight_id and run_number are required")
	}
	args := map[string]string{
		"flight_id":  sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID",
		"run_number": fmt.Sprintf("%d", m.RunNumber.ValueInt64()),
	}
	if !m.Order.IsNull() && !m.Order.IsUnknown() {
		args[`"order"`] = sqlbuild.StringLiteral(m.Order.ValueString())
	}
	limited := !m.Limit.IsNull() && !m.Limit.IsUnknown()
	offset := !m.Offset.IsNull() && !m.Offset.IsUnknown()
	suffix := ""
	switch {
	case limited && m.Limit.ValueInt64() == 0:
		args[`"limit"`] = "1"
		suffix = " LIMIT 0"
	case limited:
		args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
		if offset {
			args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
		}
	case offset:
		suffix = fmt.Sprintf(" ORDER BY line_number OFFSET %d", m.Offset.ValueInt64())
	}
	return "SELECT * FROM MD_GET_FLIGHT_LOGS" + sqlbuild.NamedArgs(args) + suffix, nil
}

func flightLogOrderValidators() []validator.String {
	return []validator.String{flightLogOrderValidator{}}
}

type flightLogOrderValidator struct{}

func (flightLogOrderValidator) Description(context.Context) string {
	return "must be one of: asc, desc"
}

func (flightLogOrderValidator) MarkdownDescription(context.Context) string {
	return "must be one of: `asc`, `desc`"
}

func (flightLogOrderValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	switch req.ConfigValue.ValueString() {
	case "asc", "desc":
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck Flight log order", "Value must be `asc` or `desc`.")
}
