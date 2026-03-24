document.addEventListener("DOMContentLoaded", function () {
  document.querySelectorAll("form[data-confirm-submit]").forEach(function (form) {
    form.addEventListener("submit", function (event) {
      var message = form.dataset.confirmSubmit;
      if (!message) {
        return;
      }

      if (!window.confirm(message)) {
        event.preventDefault();
      }
    });
  });
});

document.addEventListener("DOMContentLoaded", function () {
  var csrfMeta = document.querySelector('meta[name="csrf-token"]');

  var getDragAfterElement = function (container, itemSelector, y) {
    var items = Array.from(container.querySelectorAll(itemSelector + ":not(.is-dragging)"));
    var closest = { offset: Number.NEGATIVE_INFINITY, element: null };

    items.forEach(function (item) {
      var box = item.getBoundingClientRect();
      var offset = y - box.top - box.height / 2;
      if (offset < 0 && offset > closest.offset) {
        closest = { offset: offset, element: item };
      }
    });

    return closest.element;
  };

  var readJSONResponse = function (response) {
    return response.text().then(function (text) {
      var json = {};
      if (text) {
        try {
          json = JSON.parse(text);
        } catch (error) {
          json = {};
        }
      }

      if (!response.ok) {
        throw new Error(json.error || "request failed");
      }

      return json;
    });
  };

  document.querySelectorAll("[data-admin-reorder-list]").forEach(function (list) {
    var statusNode = list.parentElement && list.parentElement.querySelector("[data-admin-reorder-status]");
    var draggingItem = null;
    var lastSerializedOrder = "";

    var syncSerializedOrder = function () {
      lastSerializedOrder = Array.from(list.querySelectorAll("[data-admin-reorder-item]"))
        .map(function (item) { return item.dataset.slug || ""; })
        .join(",");
    };

    var sendOrder = function () {
      var params = new URLSearchParams();
      Array.from(list.querySelectorAll("[data-admin-reorder-item]")).forEach(function (item) {
        params.append("slug", item.dataset.slug || "");
      });
      params.append("stage", list.dataset.stage || "");
      params.append("module", list.dataset.module || "");
      params.append("module_order", list.dataset.moduleOrder || "");

      if (statusNode) {
        statusNode.hidden = false;
        statusNode.classList.remove("is-error");
        statusNode.textContent = "Сохраняю новый порядок...";
      }

      fetch(list.dataset.reorderEndpoint, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-CSRF-Token": csrfMeta ? csrfMeta.content : ""
        },
        body: params.toString()
      })
        .then(readJSONResponse)
        .then(function (payload) {
          if (!payload.items) {
            return;
          }

          payload.items.forEach(function (item) {
            var node = list.querySelector('[data-admin-reorder-item][data-slug="' + item.slug + '"] .admin-article-sort-title strong');
            if (!node) {
              return;
            }
            var title = node.textContent.replace(/^\d+\.\d+\s+/, "");
            node.textContent = (item.index ? item.index + " " : "") + title;
          });

          syncSerializedOrder();
          if (statusNode) {
            statusNode.textContent = "Порядок сохранен.";
          }
        })
        .catch(function (error) {
          if (statusNode) {
            statusNode.hidden = false;
            statusNode.classList.add("is-error");
            statusNode.textContent = error && error.message ? error.message : "Не удалось сохранить порядок.";
          }
        });
    };

    syncSerializedOrder();

    list.querySelectorAll("[data-admin-reorder-item]").forEach(function (item) {
      item.addEventListener("dragstart", function () {
        draggingItem = item;
        item.classList.add("is-dragging");
      });

      item.addEventListener("dragend", function () {
        item.classList.remove("is-dragging");
        draggingItem = null;

        var currentOrder = Array.from(list.querySelectorAll("[data-admin-reorder-item]"))
          .map(function (node) { return node.dataset.slug || ""; })
          .join(",");
        if (currentOrder !== lastSerializedOrder) {
          sendOrder();
        }
      });
    });

    list.addEventListener("dragover", function (event) {
      if (!draggingItem) {
        return;
      }

      event.preventDefault();
      var afterElement = getDragAfterElement(list, "[data-admin-reorder-item]", event.clientY);
      list.querySelectorAll("[data-admin-reorder-item]").forEach(function (item) {
        item.classList.remove("is-drop-target");
      });

      if (!afterElement) {
        list.appendChild(draggingItem);
        return;
      }

      afterElement.classList.add("is-drop-target");
      list.insertBefore(draggingItem, afterElement);
    });

    list.addEventListener("dragleave", function () {
      list.querySelectorAll("[data-admin-reorder-item]").forEach(function (item) {
        item.classList.remove("is-drop-target");
      });
    });

    list.addEventListener("drop", function () {
      list.querySelectorAll("[data-admin-reorder-item]").forEach(function (item) {
        item.classList.remove("is-drop-target");
      });
    });
  });

  document.querySelectorAll("[data-admin-roadmap-reorder-list]").forEach(function (list) {
    var statusNode = list.parentElement && list.parentElement.querySelector("[data-admin-roadmap-reorder-status]");
    var draggingItem = null;
    var itemKey = list.dataset.reorderKind === "modules" ? "moduleId" : "stageId";
    var paramName = list.dataset.reorderKind === "modules" ? "module_id" : "stage_id";
    var lastSerializedOrder = "";

    var syncSerializedOrder = function () {
      lastSerializedOrder = Array.from(list.querySelectorAll("[data-admin-roadmap-reorder-item]"))
        .map(function (item) { return item.dataset[itemKey] || ""; })
        .join(",");
    };

    var sendOrder = function () {
      var params = new URLSearchParams();
      Array.from(list.querySelectorAll("[data-admin-roadmap-reorder-item]")).forEach(function (item) {
        params.append(paramName, item.dataset[itemKey] || "");
      });
      if (list.dataset.reorderKind === "modules") {
        params.append("stage_id", list.dataset.stageId || "");
      }

      if (statusNode) {
        statusNode.hidden = false;
        statusNode.classList.remove("is-error");
        statusNode.textContent = "Сохраняю новый порядок...";
      }

      fetch(list.dataset.reorderEndpoint, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-CSRF-Token": csrfMeta ? csrfMeta.content : ""
        },
        body: params.toString()
      })
        .then(readJSONResponse)
        .then(function () {
          syncSerializedOrder();
          window.location.reload();
        })
        .catch(function (error) {
          if (statusNode) {
            statusNode.hidden = false;
            statusNode.classList.add("is-error");
            statusNode.textContent = error && error.message ? error.message : "Не удалось сохранить порядок.";
          }
        });
    };

    syncSerializedOrder();

    list.querySelectorAll("[data-admin-roadmap-reorder-item]").forEach(function (item) {
      item.addEventListener("dragstart", function () {
        draggingItem = item;
        item.classList.add("is-dragging");
      });

      item.addEventListener("dragend", function () {
        item.classList.remove("is-dragging");
        draggingItem = null;

        var currentOrder = Array.from(list.querySelectorAll("[data-admin-roadmap-reorder-item]"))
          .map(function (node) { return node.dataset[itemKey] || ""; })
          .join(",");
        if (currentOrder !== lastSerializedOrder) {
          sendOrder();
        }
      });
    });

    list.addEventListener("dragover", function (event) {
      if (!draggingItem) {
        return;
      }

      event.preventDefault();
      var afterElement = getDragAfterElement(list, "[data-admin-roadmap-reorder-item]", event.clientY);
      list.querySelectorAll("[data-admin-roadmap-reorder-item]").forEach(function (item) {
        item.classList.remove("is-drop-target");
      });

      if (!afterElement) {
        list.appendChild(draggingItem);
        return;
      }

      afterElement.classList.add("is-drop-target");
      list.insertBefore(draggingItem, afterElement);
    });

    list.addEventListener("dragleave", function () {
      list.querySelectorAll("[data-admin-roadmap-reorder-item]").forEach(function (item) {
        item.classList.remove("is-drop-target");
      });
    });

    list.addEventListener("drop", function () {
      list.querySelectorAll("[data-admin-roadmap-reorder-item]").forEach(function (item) {
        item.classList.remove("is-drop-target");
      });
    });
  });
});
