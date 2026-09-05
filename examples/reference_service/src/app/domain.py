from app.types import UpdatePlan


def valid_update(plan: UpdatePlan) -> bool:
    return plan.user_id > 0 and plan.amount > 0 and plan.amount <= 1000
