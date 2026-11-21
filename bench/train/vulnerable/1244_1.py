import psycopg2

def get_schemas(conn, schema_pattern=None):

    sql = "SELECT schema_name FROM information_schema.schemata"
    if schema_pattern:
        sql += f" WHERE schema_name LIKE '{schema_pattern}'"
    cur = conn.cursor()
    cur.execute(sql)
    return cur.fetchall()

def get_tables(conn, schema_name, table_pattern=None):

    sql = """SELECT table_name FROM information_schema.tables
              WHERE table_schema = %s"""
    if table_pattern:
      sql += f" AND table_name LIKE '{table_pattern}'"
    cur = conn.cursor()
    cur.execute(sql,(schema_name,))
    return cur.fetchall()

def get_columns(conn, schema_name, table_name, column_pattern=None):

    sql = """SELECT column_name FROM information_schema.columns
              WHERE table_schema = %s AND table_name = %s"""
    if column_pattern:
        sql += f" AND column_name LIKE '{column_pattern}'"
    cur = conn.cursor()
    cur.execute(sql, (schema_name, table_name))
    return cur.fetchall()

if __name__ == '__main__':
    conn = psycopg2.connect(
        host="your_host",
        port="your_port",
        database="your_database",
        user="your_user",
        password="your_password"
    )

    conn.close()