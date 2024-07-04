(ns webapp.components.logs-container
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["clipboard" :as clipboardjs]
            [re-frame.core :as rf]
            [webapp.components.headings :as h]))

;; TODO: move it to another component
(defn copy-clipboard [data-clipboard-target]
  [:div {:class (str "copy-to-clipboard absolute rounded-lg p-x-small "
                     "top-2 right-2 cursor-pointer box-border "
                     "opacity-0 group-hover:opacity-100 transition z-20")
         :data-clipboard-target data-clipboard-target}
   [:> hero-outline-icon/ClipboardIcon {:class "h-6 w-6 shrink-0 text-white"
                                        :aria-hidden "true"}]])

(defmulti logs-area identity)
(defmethod logs-area :success [_ logs] logs)
(defmethod logs-area :loading [_ _]
  [:div.flex.gap-small
   [:span "loading"]
   [:figure.w-4
    [:img.animate-spin {:src "/icons/icon-loader-circle-white.svg"}]]])
(defmethod logs-area :failure [_ _] "There was an error to get the logs for this task")
(defmethod logs-area :default [_ _] "No logs to show")

(defn container
  "config is a map with the following fields:
  :status -> possible values are :success :loading :failure. Anything different will be default to an generic error message
  :id -> id to differentiate more than one log on the same page.
  :logs -> the actual string with the logs"
  [config title]
  (let [clipboard (new clipboardjs ".copy-to-clipboard")
        container-id (or (:id config) "task-logs")]
    (.on clipboard "success" #(rf/dispatch [:show-snackbar {:level :success :text "Text copied to clipboard"}]))
    [:div {:class "h-5/6"}
     (when title [h/h3 title {:class "mb-regular"}])
     [:section
      {:class (str "relative rounded-lg bg-gray-100 h-full"
                   " font-mono p-regular text-xs mb-regular"
                   " whitespace-pre group")}
      (when-not (:not-clipboard? config) (copy-clipboard (str "#" container-id)))
      [:div
       {:id container-id
        :class (str (when (:classe config) (:classes config))
                    " overflow-auto whitespace-pre h-full"
                    (when-not (:fixed-height? config) " max-h-80"))}
       (logs-area (:status config) (:logs config))]]]))

(defn new-container
  "config is a map with the following fields:
  :status -> possible values are :success :loading :failure. Anything different will be default to an generic error message
  :id -> id to differentiate more than one log on the same page.
  :logs -> the actual string with the logs"
  [config title]
  (let [clipboard (new clipboardjs ".copy-to-clipboard")
        container-id (or (:id config) "task-logs")]
    (.on clipboard "success" #(rf/dispatch [:show-snackbar {:level :success :text "Text copied to clipboard"}]))
    [:div {:class "h-full overflow-auto"}
     (when title [h/h3 title {:class "mb-regular"}])
     [:section
      {:class (str "relative bg-gray-900 font-mono overflow-auto h-full"
                   " whitespace-pre text-gray-200 text-sm"
                   " px-regular pt-regular group")}
      (when-not (:not-clipboard? config) (copy-clipboard (str "#" container-id)))
      [:div
       {:id container-id
        :class (str (when (:classes config) (:classes config))
                    " overflow-auto whitespace-pre h-full"
                    (when-not (:fixed-height? config) " max-h-80"))}
       (logs-area (:status config) (:logs config))]]]))
