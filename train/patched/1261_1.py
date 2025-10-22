import snowflake.connector
from snowflake.connector import DictCursor
from snowflake.connector.pandas_tools import write_pandas
import pandas as pd

def fixed_write_pandas(conn, df, table_name, database=None, schema=None):


    if database:
        conn.cursor().execute(f"USE DATABASE {database}")
    if schema:
       conn.cursor().execute(f"USE SCHEMA {schema}")

    columns = df.columns
    placeholders = ", ".join(["%s"] * len(columns))
    column_names = ", ".join([f'"{col}"' for col in columns])

    sql = f"INSERT INTO {table_name} ({column_names}) VALUES ({placeholders})"

    data = [tuple(row) for row in df.values.tolist()]
    with conn.cursor() as cursor:
        cursor.executemany(sql, data)

if __name__ == '__main__':
    conn = snowflake.connector.connect(
        user='your_user',
        password='your_password',
        account='your_account',
        warehouse='your_warehouse',
        database='your_database',
        schema='your_schema'
    )

    data = {'name': ['Alice', 'Bob', 'Charlie'],
            'age': [25, 30, 28],
             'city': ["New York", "London", "Paris"]
           }
    df = pd.DataFrame(data)

    table_name = 'my_table'

    try:
        with conn.cursor() as cursor:
            cursor.execute(f"CREATE OR REPLACE TABLE {table_name} (name STRING, age INT, city STRING)")
    except Exception as e:
            print (f"Error creating table: {e}")

    try:
        fixed_write_pandas(conn, df, table_name)
        print("Data written to Snowflake successfully!")
    except Exception as e:
        print(f"Error writing to Snowflake: {e}")

    conn.close()