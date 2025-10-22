def vulnerable_mcopy(src, dest):

    dest.clear()
    dest.extend(src)

destination_buffer = []
vulnerable_mcopy("A crafted string that could potentially cause issues" * 100, destination_buffer)