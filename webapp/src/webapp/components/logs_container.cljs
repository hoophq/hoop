(ns webapp.components.logs-container
  (:require
   ["@radix-ui/themes" :refer [ScrollArea]]
   ["lucide-react" :refer [Clipboard]]
   ["clipboard" :as clipboardjs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.headings :as h]
   [webapp.config :as config]))


(defmulti logs-area identity)
(defmethod logs-area :success [_ logs] logs)
(defmethod logs-area :loading [_ _]
  [:div.flex.gap-small
   [:span "loading"]
   [:figure.w-4
    [:img.animate-spin {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]])
(defmethod logs-area :failure [_ _] "There was an error to get the logs for this task")
(defmethod logs-area :default [_ _] "No logs to show")

(defn new-container
  "config is a map with the following fields:
  :status -> possible values are :success :loading :failure. Anything different will be default to an generic error message
  :id -> id to differentiate more than one log on the same page.
  :logs -> the actual string with the logs"
  [config title]
  (let [container-id (or (:id config) "task-logs")
        unique-clipboard-class (str "copy-to-clipboard-" container-id)]

    (r/with-let [clipboard (new clipboardjs (str "." unique-clipboard-class))]
      ;; Setup clipboard success handler
      (.on clipboard "success" #(rf/dispatch [:show-snackbar {:level :success :text "Text copied to clipboard"}]))

      ;; Render component
      [:div {:class "h-full overflow-auto"}
       (when title [h/h3 title {:class "mb-regular"}])
       [:section
        {:class (str "relative bg-gray-900 font-mono overflow-auto h-full"
                     " text-gray-200 text-sm"
                     " p-radix-4 rounded-lg group"
                     (when (:whitespace? config) " whitespace-pre"))
         :on-copy (when (:not-clipboard? config)
                    (fn [e] (.preventDefault e)))}
        (when-not (:not-clipboard? config)
          [:div {:class (str unique-clipboard-class " absolute rounded-lg p-x-small "
                             "top-2 right-2 cursor-pointer box-border "
                             "opacity-0 group-hover:opacity-100 transition z-20")
                 :data-clipboard-target (str "#" container-id)}
           [:> Clipboard {:size 16}]])
        [:> ScrollArea {:size "2"
                        :class "dark"}
         [:div
          {:id container-id
           :class (str (when (:classes config) (:classes config))
                       " h-full"
                       (when-not (:fixed-height? config) " max-h-80")
                       (when (:not-clipboard? config) " select-none"))
           :style (when (:not-clipboard? config)
                    #js {:WebkitUserSelect "none"
                         :MozUserSelect "none"
                         :msUserSelect "none"
                         :userSelect "none"})}
          (logs-area (:status config) (:logs config))]]]]

      ;; Cleanup on unmount
      (finally
        (.destroy clipboard)))))
