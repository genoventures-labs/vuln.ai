class UserHandler:
    def __init__(self, db):
        self.db = db

    def delete_user(self, user_id):
        # Potential SQL Injection via string formatting
        query = "DELETE FROM users WHERE id = '%s'" % user_id
        self.db.execute(query)

def helper_func():
    print("This is a helper function that should be its own chunk")
